package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	sqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	_ "github.com/oxplot/starenv/autoload"
)

const lambdafyEnvPrefix = "LAMBDAFY_"

var (
	port     int    // port that proxy will proxy requests to
	endpoint string // end point that proxy will proxy requests to
	inLambda = os.Getenv("AWS_LAMBDA_FUNCTION_VERSION") != "" && os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" && os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != ""
	reqCount int32
	started  = make(chan struct{})

	client = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

func init() {
	// Generate a random port number between 19000 and 19999.
	// This is to ensure the user program can't depend on hardcoded port numbers.
	port = 19000 + int(time.Now().UnixNano()%1000)
	endpoint = "127.0.0.1:" + strconv.Itoa(port)
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
			return nil, err
		}
		handleSQS(ctx, sqsEvent)
		return nil, nil

	} else if _, ok := e["rawQueryString"]; ok {
		var httpEvent events.APIGatewayV2HTTPRequest
		if err := json.Unmarshal(b, &httpEvent); err != nil {
			return nil, err
		}
		return handleHTTP(ctx, httpEvent)
	}

	return nil, fmt.Errorf("event type %v not supported by this lambda function", e)
}

var sqsARNPat = regexp.MustCompile(`^arn:aws:sqs:([^:]+):([^:]+):(.+)$`)

func getSQSQueueURL(arn string) string {
	m := sqsARNPat.FindStringSubmatch(arn)
	if m == nil {
		return "INVALID ARN"
	}
	return fmt.Sprintf("https://sqs.%s.amazonaws.com/%s/%s", m[1], m[2], m[3])
}

// handleSQS handles SQS events and translates them to HTTP requests to the user
// program. The events are sent as POST requests to /_lambdafy/sqs with the SQS
// event body as the HTTP payload. A 2xx response from the user program is
// considered a success and the event is deleted from the queue. A non-2xx
// response is considered a failure and the event is left in the queue for
// retry.
func handleSQS(ctx context.Context, e events.SQSEvent) {

	// We remove messages from the queue ourselves instead of returning failure
	// responses. Any message not deleted will be retried by SQS.

	c, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Printf("error loading AWS config: %v", err)
		return
	}
	sqsCl := sqs.NewFromConfig(c)

	// Make n simultaneous requests to the user program to process the SQS records
	// in the batch. To avoid overwhelming the user program, ensure the trigger is
	// configured with small batch sizes.

	wg := sync.WaitGroup{}
	wg.Add(len(e.Records))

	for _, r := range e.Records {
		go func(r events.SQSMessage) {
			defer wg.Done()

			// Build standard HTTP request from the SQS event

			u, _ := url.Parse(fmt.Sprintf("http://%s/_lambdafy/sqs", endpoint))
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(r.Body))
			if err != nil {
				log.Printf("error creating HTTP request for SQS msg %s: %v", r.MessageId, err)
				return
			}
			req.Header.Add("Content-Length", strconv.Itoa(len(r.Body)))
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("error sending HTTP request for SQS msg %s: %v", r.MessageId, err)
				return
			}
			defer resp.Body.Close()

			// If success, delete the message from the queue.

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				_, err := sqsCl.DeleteMessage(ctx, &sqs.DeleteMessageInput{
					QueueUrl:      aws.String(getSQSQueueURL(r.EventSourceARN)),
					ReceiptHandle: &r.ReceiptHandle,
				})
				if err != nil {
					log.Printf("error deleting processed SQS msg %s: %v", r.MessageId, err)
				}
				return
			}

			// If failed, log the response status and body

			log.Printf("failed to process SQS msg %s: %s", r.MessageId, resp.Status)
			b, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				log.Printf("error reading response body for SQS msg %s: %v", r.MessageId, err)
				return
			}
			log.Print(string(b))

		}(r)
	}

	wg.Wait()
}

// handleHTTP handles API Gateway HTTP events and translates them to HTTP
// requests to the user program.
func handleHTTP(ctx context.Context, req events.APIGatewayV2HTTPRequest) (res events.APIGatewayV2HTTPResponse, err error) {

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
	u, _ := url.Parse(fmt.Sprintf("http://%s%s%s", endpoint, req.RawPath, req.RawQueryString))

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

func run() (exitCode int, err error) {
	if len(os.Args) < 2 {
		return 127, fmt.Errorf("usage: %s command [arg [arg [...]]]", os.Args[0])
	}
	cmdName := os.Args[1]

	// Remove all env vars with lambdafy prefix to prevent child process from
	// depending on them.

	for _, e := range os.Environ() {
		if strings.HasPrefix(e, lambdafyEnvPrefix) {
			os.Unsetenv(strings.SplitN(e, "=", 2)[0])
		}
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

	log.Printf("running in lambda, starting proxying traffic to %s", endpoint)

	args := os.Args[2:]

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start listening for traffic as soon as possible, otherwise lambda will
	// throw timeout errors.

	lambdaStopped := make(chan struct{})

	go func() {
		defer close(lambdaStopped)
		lambda.StartWithContext(ctx, handle)
	}()

	// Set/override the PORT env var

	os.Setenv("PORT", strconv.Itoa(port))

	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return 127, fmt.Errorf("failed to run command: %s", err)
	}

	// Exit when the child process exits

	go func() {
		if err := cmd.Wait(); err != nil {
			if err, ok := err.(*exec.ExitError); ok {
				log.Printf("command exited with code: %d", err.ExitCode())
			} else {
				log.Printf("error: waiting for command: %s", err)
			}
		}
		cancel()
	}()

	// Pass through all signals to the child process

	sigs := make(chan os.Signal, 1)
	go func() {
		for s := range sigs {
			if cmd.Process != nil { // to ignore signals while subcmd is launching
				_ = cmd.Process.Signal(s)
			}
		}
	}()
	signal.Notify(sigs)

	// Wait until the upstream is up and running

	waitClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	log.Printf("waiting for startup request to succeed")

	for {
		u := "http://" + endpoint + "/"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return 1, fmt.Errorf("failed to create startup request: %s", err)
		}
		if resp, err := waitClient.Do(req); err == nil {
			resp.Body.Close()
			break
		}
		select {
		case <-ctx.Done():
			return 1, fmt.Errorf("startup request aborted due to cancelled context")
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	log.Printf("startup request passed - proxying requests from now on")

	// Unblock the request handler

	close(started)

	// Wait for lambda listener to stop (or the main context to be cancelled)

	select {
	case <-lambdaStopped:
	case <-ctx.Done():
	}

	// Terminate child process when the proxy exits - usually due to error

	cancel()
	signal.Stop(sigs)
	close(sigs)

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
