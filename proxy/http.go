package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

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
