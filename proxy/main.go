package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/oxplot/starenv"
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
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
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

// run is the main entry point for the proxy.
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

	envLoader := starenv.NewLoader()
	for t, n := range starenv.DefaultDerefers {
		envLoader.Register(t, &starenv.LazyDerefer{New: n})
	}
	envLoader.Register(sendSQSStarenvTag, sqsIDToQueueURL)

	if err := envLoader.Load(); len(err) > 0 {
		return 1, fmt.Errorf("error loading env vars: %s", err)
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
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	log.Printf("waiting for startup request to succeed")

StartupRequest:
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
			break StartupRequest
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
