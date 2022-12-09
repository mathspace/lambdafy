package main

import (
	"bytes"
	"compress/gzip"
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

	client = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

// handler is the Lambda function handler
func handler(req events.APIGatewayV2HTTPRequest) (res events.APIGatewayV2HTTPResponse, err error) {

	<-started

	reqNum := atomic.AddInt32(&reqCount, 1)

	if verbose {
		log.Printf("incoming req #%d : %#v", reqNum, req)
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
	u, _ := url.Parse(fmt.Sprintf("http://%s%s%s", endpoint, req.RawPath, req.RawQueryString))

	r, err := http.NewRequest(req.RequestContext.HTTP.Method, u.String(), strings.NewReader(body))
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

	if verbose {
		log.Printf("proxied req #%d : %#v", reqNum, r)
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

	if verbose {
		log.Printf("proxied res #%d : %#v", reqNum, s)
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

	if verbose {
		log.Printf("outgoing res #%d : %#v", reqNum, res)
	}

	return
}

func run() (exitCode int, err error) {
	if len(os.Args) < 2 {
		return 127, fmt.Errorf("usage: %s command [arg [arg [...]]]", os.Args[0])
	}
	cmdName := os.Args[1]

	if !inLambda {
		if verbose {
			log.Print("not running in lambda, exec the command directly")
		}
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

	args := os.Args[2:]

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
