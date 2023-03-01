package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	sqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/oxplot/starenv"
	"github.com/oxplot/starenv/derefer"
)

const lambdafyEnvPrefix = "LAMBDAFY_"

var (
	port            int    // base port for various endpoints
	appEndpoint     string // end point that proxy will proxy requests to
	listen          string // listen address for our own HTTP server used for proxying to AWS services.
	functionName    = os.Getenv("AWS_LAMBDA_FUNCTION_NAME")
	functionVersion = os.Getenv("AWS_LAMBDA_FUNCTION_VERSION")
	inLambda        = functionName != "" && functionVersion != "" && os.Getenv("AWS_LAMBDA_RUNTIME_API") != ""
	started         = make(chan struct{})

	client = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

func init() {
	rand.Seed(time.Now().UnixNano())
	// Generate a random port number between 19000 and 19999.
	// This is to ensure the user program can't depend on hardcoded port numbers.
	port = 19000 + int(time.Now().UnixNano()%1000)
	appEndpoint = "127.0.0.1:" + strconv.Itoa(port)
	listen = "127.0.0.1:" + strconv.Itoa(port+1)
}

// handle is a generic handler for all Lambda events supported by this function.
func handle(ctx context.Context, e map[string]json.RawMessage) (any, error) {

	// Once started channel is closed, this will unblock for all future requests.
	<-started

	// Flush stdout and stderr before returning to ensure the logs are captured by
	// AWS.
	defer func() {
		os.Stdout.Sync()
		os.Stderr.Sync()
	}()

	b, _ := json.Marshal(e)

	if _, ok := e["Records"]; ok { // SQS event
		var sqsEvent events.SQSEvent
		if err := json.Unmarshal(b, &sqsEvent); err != nil {
			log.Printf("failed to unmarshal the SQS event: %v", err)
			return nil, err
		}
		return handleSQS(ctx, sqsEvent)

	} else if _, ok := e["rawQueryString"]; ok {
		var httpEvent events.APIGatewayV2HTTPRequest
		if err := json.Unmarshal(b, &httpEvent); err != nil {
			log.Printf("failed to unmarshal the APIGatewayV2 event: %v", err)
			return nil, err
		}
		return handleHTTP(ctx, httpEvent)

	} else if _, ok := e["cron"]; ok {
		var cronEvent struct {
			Cron string `json:"cron"`
		}
		if err := json.Unmarshal(b, &cronEvent); err != nil {
			log.Printf("failed to unmarshal the cron event: %v", err)
		}
		return nil, handleCron(ctx, cronEvent.Cron)
	}

	return nil, fmt.Errorf("event type %v not supported by this lambda function", e)
}

func handleCron(ctx context.Context, cronName string) error {
	u := fmt.Sprintf("http://%s/_lambdafy/cron?name=%s", appEndpoint, url.QueryEscape(cronName))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return fmt.Errorf("error creating HTTP request for cron '%s': %v", cronName, err)
	}
	req.Header.Add("Content-Length", "0")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending HTTP request for cron '%s': %v", cronName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("error sending HTTP request for cron '%s': %v", cronName, resp.Status)
	}
	return nil
}

var sqsARNPat = regexp.MustCompile(`^arn:aws:sqs:([^:]+):([^:]+):(.+)$`)

// getSQSQueueURL returns the URL of the SQS queue given its ARN.
func getSQSQueueURL(arn string) string {
	m := sqsARNPat.FindStringSubmatch(arn)
	if m == nil {
		return ""
	}
	return fmt.Sprintf("https://sqs.%s.amazonaws.com/%s/%s", m[1], m[2], m[3])
}

// handleSQS handles SQS events and translates them to HTTP requests to the user
// program. The events are sent as POST requests to /_lambdafy/sqs with the SQS
// event body as the HTTP payload. A 2xx/3xx response from the user program is
// considered a success and the event is deleted from the queue. A non-2xx/3xx
// response is considered a failure and the event is left in the queue for
// retry.
func handleSQS(ctx context.Context, e events.SQSEvent) (resp events.SQSEventResponse, err error) {

	log.Printf("processing batch of %d SQS records", len(e.Records))

	type taskResult struct {
		msgID string
		err   error
	}
	taskResults := make(chan taskResult)

	// Make n simultaneous requests to the user program to process the SQS records
	// in the batch. To avoid overwhelming the user program, ensure the trigger is
	// configured with small batch sizes.

	for _, r := range e.Records {
		go func(r events.SQSMessage) {

			err := func() error {
				// Build standard HTTP request from the SQS event

				u, _ := url.Parse(fmt.Sprintf("http://%s/_lambdafy/sqs", appEndpoint))
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(r.Body))
				if err != nil {
					return fmt.Errorf("error creating HTTP request: %v", err)
				}
				req.Header.Add("Content-Length", strconv.Itoa(len(r.Body)))
				resp, err := client.Do(req)
				if err != nil {
					return fmt.Errorf("error sending HTTP request: %v", err)
				}
				defer resp.Body.Close()

				// Success

				if resp.StatusCode >= 200 && resp.StatusCode < 400 {
					return nil
				}

				// Failure

				b, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("error reading response body: %v", err)
				}
				return fmt.Errorf("non-2xx/3xx response: %s", string(b))
			}()
			taskResults <- taskResult{msgID: r.MessageId, err: err}

		}(r)
	}

	for range e.Records {
		res := <-taskResults
		if res.err == nil {
			continue
		}
		log.Printf("failed to process SQS msg %s: %s", res.msgID, res.err)
		resp.BatchItemFailures = append(resp.BatchItemFailures, events.SQSBatchItemFailure{
			ItemIdentifier: res.msgID,
		})
	}

	if len(resp.BatchItemFailures) > 0 {
		return resp, fmt.Errorf("some requests failed")
	}
	return resp, nil
}

// handleHTTP handles API Gateway HTTP events and translates them to HTTP
// requests to the user program.
func handleHTTP(ctx context.Context, req events.APIGatewayV2HTTPRequest) (res events.APIGatewayV2HTTPResponse, err error) {

	// Ignore special /_lambdafy paths

	if strings.HasPrefix(req.RawPath, "/_lambdafy/") {
		res.StatusCode = http.StatusNotFound
		return
	}

	// Build standard HTTP request from the API Gateway request

	body := req.Body
	if req.IsBase64Encoded {
		var b []byte
		b, err = base64.StdEncoding.DecodeString(body)
		if err != nil {
			return
		}
		body = string(b)
	}

	if req.RawPath == "" {
		req.RawPath = "/"
	}
	if req.RawQueryString != "" {
		req.RawQueryString = "?" + req.RawQueryString
	}
	u, _ := url.Parse(fmt.Sprintf("http://%s%s%s", appEndpoint, req.RawPath, req.RawQueryString))

	r, err := http.NewRequestWithContext(ctx, req.RequestContext.HTTP.Method, u.String(), strings.NewReader(body))
	if err != nil {
		return
	}
	r.Header.Add("Content-Length", strconv.Itoa(len(body)))
	gzipAllowed := false
	for k, v := range req.Headers {
		k = strings.ToLower(k)
		switch k {
		case "host":
			r.Host = v
		case "accept-encoding":
			// We do our own compression.
			if strings.Contains(v, "gzip") {
				gzipAllowed = true
			}
		default:
			r.Header.Add(k, v)
		}
	}

	s, err := client.Do(r)
	if err != nil {
		return
	}
	defer s.Body.Close()

	// Build API Gateway response from standard HTTP response

	resBody, err := io.ReadAll(s.Body)
	if err != nil {
		return
	}

	res.Headers = map[string]string{}
	res.MultiValueHeaders = map[string][]string{}

	// We do our own compression if the client supports it and the upstream
	// response is not already compressed.

	if gzipAllowed && s.Header.Get("Content-Encoding") == "" {
		gzBody := &bytes.Buffer{}
		gw := gzip.NewWriter(gzBody)
		_, _ = gw.Write(resBody)
		_ = gw.Close()
		resBody = gzBody.Bytes()
		res.Headers["Content-Encoding"] = "gzip"
	}

	res.StatusCode = s.StatusCode
	res.IsBase64Encoded = true
	res.Body = base64.StdEncoding.EncodeToString(resBody)
	for k, vs := range s.Header {
		if strings.ToLower(k) == "set-cookie" {
			res.Cookies = append(res.Cookies, vs...)
		} else if len(vs) == 1 {
			res.Headers[k] = vs[0]
		} else {
			for _, v := range vs {
				res.MultiValueHeaders[k] = append(res.MultiValueHeaders[k], v)
			}
		}
	}

	return
}

type sqsSendDerefer map[string]string

// Deref generates a new random ID and maps it to the queue URL of the given SQS
// ARN, and adds it to the map. It returns a URL that the user program can use
// to send messages to the queue.
func (d sqsSendDerefer) Deref(arn string) (string, error) {
	// Generate a random string ID.
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	idStr := hex.EncodeToString(id)
	qURL := getSQSQueueURL(arn)
	if qURL == "" {
		return "", fmt.Errorf("invalid SQS ARN: %s", arn)
	}
	d[idStr] = qURL
	return fmt.Sprintf("http://%s/sqs?id=%s", listen, idStr), nil
}

// sqsIdToQueueURL maps randomly generated IDs to queue URLs. Random IDs
// ensure the user program cannot rely on the URL staying the same over time.
var sqsIDToQueueURL = sqsSendDerefer{}

const sqsGroupIDHeader = "Lambdafy-SQS-Group-Id"

// handleSQSSend handles HTTP POST requests and translates them to SQS send
// message.
// Lambdafy-SQS-Group-Id header is used to set the message group ID.
func handleSQSSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	qID := r.URL.Query().Get("id")
	if qID == "" {
		http.Error(w, "Missing queue ID", http.StatusBadRequest)
		return
	}

	qURL, ok := sqsIDToQueueURL[qID]
	if !ok {
		http.Error(w, "Invalid queue ID", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}

	var groupID *string
	if g := r.Header.Get(sqsGroupIDHeader); g != "" {
		groupID = &g
	}

	c, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Printf("error loading AWS config: %v", err)
		http.Error(w, fmt.Sprintf("Error loading AWS config: %v", err), http.StatusInternalServerError)
		return
	}
	sqsCl := sqs.NewFromConfig(c)

	if _, err := sqsCl.SendMessage(context.Background(), &sqs.SendMessageInput{
		MessageBody:    aws.String(string(body)),
		QueueUrl:       aws.String(qURL),
		MessageGroupId: groupID,
	}); err != nil {
		log.Printf("error sending SQS message: %v", err)
		http.Error(w, fmt.Sprintf("Error sending SQS message: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("sent an SQS message to '%s'", qURL)

}

const sendSQSStarenvTag = "lambdafy_sqs_send"

func run() (exitCode int, err error) {
	if len(os.Args) < 2 {
		return 127, fmt.Errorf("usage: %s command [arg [arg [...]]]", os.Args[0])
	}
	cmdName := os.Args[1]

	// Remove all env vars with lambdafy prefix to prevent child process from
	// depending on them.
	// IMPORTANT: This must come before startenv loading since none of the values
	// in the lambdafy prefixed env vars are meant be dereferenced.

	for _, e := range os.Environ() {
		if strings.HasPrefix(e, lambdafyEnvPrefix) {
			os.Unsetenv(strings.SplitN(e, "=", 2)[0])
		}
	}

	// Load env vars/derefence them from various sources

	for t, n := range derefer.NewDefault {
		starenv.Register(t, &derefer.Lazy{New: n})
	}
	starenv.Register(sendSQSStarenvTag, sqsIDToQueueURL)

	if err := starenv.Load(); err != nil {
		return 1, fmt.Errorf("error loading env vars: %w", err)
	}

	if !inLambda {
		path, err := exec.LookPath(cmdName)
		if err != nil {
			return 1, fmt.Errorf("cannot find command '%s': %w", cmdName, err)
		}
		// syscall.Exec requires the first argument to be the command name.
		args := os.Args[1:]
		err = syscall.Exec(path, args, os.Environ())
		// If Exec succeeds, we'll never get here.
		return 1, err
	}

	log.Printf("running in lambda, starting proxying traffic to %s", appEndpoint)

	args := os.Args[2:]

	// Start own AWS proxy endpoint (used for sending on SQS and other services)

	http.HandleFunc("/sqs", handleSQSSend)
	go http.ListenAndServe(listen, nil)

	// Set/override the PORT env var

	os.Setenv("PORT", strconv.Itoa(port))

	// Run the command

	cmd := exec.Command(cmdName, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return 127, fmt.Errorf("failed to run command: %s", err)
	}

	// Pass through all signals to the child process

	sigs := make(chan os.Signal)
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s)
		}
	}()
	signal.Notify(sigs)

	// Monitor child process for when it exits.

	processStopped := make(chan struct{})
	go func() {
		defer close(processStopped)
		if err := cmd.Wait(); err != nil {
			if err, ok := err.(*exec.ExitError); ok {
				log.Printf("command exited with code: %d", err.ExitCode())
			} else {
				log.Printf("error: waiting for command: %s", err)
			}
		}
		os.Stdout.Sync()
		os.Stderr.Sync()
	}()

	// Start listening for traffic as soon as possible, otherwise lambda will
	// throw timeout errors.
	// If start fails, it rudely kills the process so no need to do anything here.
	// Inside a container, once we are killed, so will every other process, so no
	// need to do anything here to catch it.

	go lambda.StartWithOptions(handle, lambda.WithEnableSIGTERM())

	// Wait until the upstream is up and running

	waitClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	log.Printf("waiting for startup request to succeed")

	for {
		u := "http://" + appEndpoint + "/"
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return 1, fmt.Errorf("failed to create startup request: %s", err)
		}
		if resp, err := waitClient.Do(req); err == nil {
			resp.Body.Close()
			log.Printf("startup request passed - proxying requests from now on")
			close(started) // Unblock the request handler
			break
		}
		select {
		case <-processStopped:
			break
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Wait for process/lambda to stop.

	<-processStopped

	if cmd.ProcessState.ExitCode() == -1 {
		return 127, nil
	}
	return cmd.ProcessState.ExitCode(), nil
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("lambdafy-proxy: ")
	exitCode, err := run()
	if err != nil {
		log.Fatalf("error: %s", err)
	}
	os.Exit(exitCode)
}
