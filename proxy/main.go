package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	_ "github.com/oxplot/starenv/autoload"
)

const port = 19987

var (
	endpoint = "127.0.0.1:" + strconv.Itoa(port)
	verbose  = os.Getenv("LAMBDAFY_PROXY_LOGGING") == "verbose"
	inLambda = os.Getenv("LAMBDA_TASK_ROOT") != ""
	reqCount int32
	started  = make(chan struct{})
)

// handler is the Lambda function handler
func handler(req events.APIGatewayV2HTTPRequest) (res events.APIGatewayV2HTTPResponse, err error) {

	<-started

	reqNum := atomic.AddInt32(&reqCount, 1)

	if verbose {
		log.Printf("incoming req #%d : %#v", reqNum, req)
	}

	// Build standard HTTP request from the API Gateway request

	var body io.Reader
	body = strings.NewReader(req.Body)
	if req.IsBase64Encoded {
		body = base64.NewDecoder(base64.StdEncoding, body)
	}

	if req.RawPath == "" {
		req.RawPath = "/"
	}
	if req.RawQueryString != "" {
		req.RawQueryString = "?" + req.RawQueryString
	}
	u, _ := url.Parse(fmt.Sprintf("http://%s%s%s", endpoint, req.RawPath, req.RawQueryString))

	r, err := http.NewRequest(req.RequestContext.HTTP.Method, u.String(), body)
	if err != nil {
		return
	}
	for k, v := range req.Headers {
		if strings.ToLower(k) == "host" {
			r.Host = v
		} else {
			r.Header.Add(k, v)
		}
	}

	if verbose {
		log.Printf("proxied req #%d : %#v", reqNum, r)
	}

	s, err := http.DefaultClient.Do(r)
	if err != nil {
		return
	}
	defer s.Body.Close()

	// Build API Gateway response from standard HTTP response

	resBody, err := io.ReadAll(s.Body)
	if err != nil {
		return
	}

	res.StatusCode = s.StatusCode
	res.IsBase64Encoded = true
	res.Body = base64.StdEncoding.EncodeToString(resBody)
	res.Headers = map[string]string{}
	res.MultiValueHeaders = map[string][]string{}
	for k, vs := range s.Header {
		if len(vs) == 1 {
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
	args := os.Args[1:]

	if !inLambda {
		if verbose {
			log.Print("not running in lambda, exec the command directly")
		}
		path, err := exec.LookPath(cmdName)
		if err != nil {
			return 1, fmt.Errorf("cannot find command '%s': %w", cmdName, err)
		}
		err = syscall.Exec(path, args, os.Environ())
		// If Exec succeeds, we'll never get here.
		return 1, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start listening for traffic as soon as possible, otherwise lambda will
	// throw timeout errors.

	lambdaStopped := make(chan struct{})

	go func() {
		defer close(lambdaStopped)
		lambda.StartWithContext(ctx, handler)
	}()

	portStr := strconv.Itoa(port)

	// Replace @@LAMBDAFY_PORT@@ in all arguments

	for i, a := range args {
		args[i] = strings.ReplaceAll(a, "@@LAMBDAFY_PORT@@", portStr)
	}

	// Duplicate own env and add PORT to it, replacing all occurrences of
	// @@LAMBDAFY_PORT@@

	env := make([]string, 0, len(os.Environ())+1)
	for _, v := range os.Environ() {
		if strings.HasPrefix(v, "PORT=") {
			continue
		}
		v = strings.ReplaceAll(v, "@@LAMBDAFY_PORT@@", portStr)
		env = append(env, v)
	}
	env = append(env, fmt.Sprintf("PORT=%d", port))

	// If running outside of lambda, simply replace the current process

	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		cancel()
		return 127, fmt.Errorf("failed to run command: %s", err)
	}

	// Exit when the child process exits

	go func() {
		cmd.Wait()
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
	for {
		if _, err := waitClient.Get("http://" + endpoint); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			break
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Unblock the request handler

	close(started)

	// Wait for lambda listener to stop

	<-lambdaStopped

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
	exitCode, err := run()
	if err != nil {
		log.Fatalf("lambdafy-proxy: error: %s", err)
	}
	os.Exit(exitCode)
}
