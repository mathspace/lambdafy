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
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	_ "github.com/oxplot/starenv/autoload"
)

const port = 19987

var (
	endpoint = "127.0.0.1:" + strconv.Itoa(port)
	client   = &http.Client{}
	verbose  = os.Getenv("LAMBDAFY_PROXY_LOGGING") == "verbose"
	reqCount int32
)

func handler(req events.APIGatewayV2HTTPRequest) (res events.APIGatewayV2HTTPResponse, err error) {

	if verbose {
		log.Printf("req #%d : %#v", atomic.AddInt32(&reqCount, 1), req)
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
		r.Header.Add(k, v)
	}

	s, err := client.Do(r)
	if err != nil {
		return
	}

	// Build API Gateway response from standard HTTP response

	resBody, err := io.ReadAll(s.Body)
	if err != nil {
		return
	}
	s.Body.Close()

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
	args := os.Args[2:]
	ctx, cancel := context.WithCancel(context.Background())

	portStr := strconv.Itoa(port)

	// Replace @@LAMBDAFY_PORT@@ in all arguments

	for i, a := range args {
		args[i] = strings.ReplaceAll(a, "@@LAMBDAFY_PORT@@", portStr)
	}

	// Duplicate own env and add PORT to it, replacing all occurrences of
	// @@LAMBDAFY_PORT@@

	env := make([]string, 0, len(os.Environ()))
	for _, v := range os.Environ() {
		if strings.HasPrefix(v, "PORT=") {
			continue
		}
		env = append(env, strings.ReplaceAll(v, "@@LAMBDAFY_PORT@@", portStr))
	}
	env = append(env, fmt.Sprintf("PORT=%d", port))

	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return 127, fmt.Errorf("failed to run command: %s", err)
	}

	// Exit when the child process exits

	go func() {
		cmd.Wait()
		cancel()
	}()

	// Pass through all signals to the child process

	sigs := make(chan os.Signal)
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
			time.Sleep(25 * time.Millisecond)
		}
	}

	lambda.StartWithContext(ctx, handler)

	// Terminate child process when the proxy exits - usually due to error
	cancel()
	signal.Stop(sigs)
	close(sigs)

	if cmd.ProcessState.ExitCode() == -1 {
		return 127, nil
	} else {
		return cmd.ProcessState.ExitCode(), nil
	}
}

func main() {
	log.SetFlags(0)
	exitCode, err := run()
	if err != nil {
		log.Fatal(err)
	}
	os.Exit(exitCode)
}
