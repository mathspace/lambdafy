package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/oxplot/starenv"
)

type awsTestHTTPClient func(*http.Request) (*http.Response, error)

func (f awsTestHTTPClient) Do(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise real SDK middleware with fake credentials and no AWS network access.
// Incompatible SDK modules can compile but fail before sending any request.
func TestAWSClientCompatibility(t *testing.T) {
	for _, service := range []string{"ssm", "sqs", "sts"} {
		t.Run(service, func(t *testing.T) {
			requests := 0
			cfg := aws.Config{
				Region:      "ap-southeast-2",
				Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
				HTTPClient: awsTestHTTPClient(func(r *http.Request) (*http.Response, error) {
					requests++
					if !strings.Contains(r.Header.Get("Authorization"), "/ap-southeast-2/"+service+"/aws4_request") {
						t.Error("request was not signed for the configured region and service")
					}
					body, contentType := `{}`, "application/x-amz-json-1.0"
					switch service {
					case "ssm":
						var input struct {
							Name           string
							WithDecryption bool
						}
						if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
							t.Fatal(err)
						}
						if input.Name != "/new-sana/api-key" || !input.WithDecryption {
							t.Fatalf("unexpected parameter request: %+v", input)
						}
						body = `{"Parameter":{"Name":"/new-sana/api-key","Type":"SecureString","Value":"test-value"}}`
						contentType = "application/x-amz-json-1.1"
					case "sts":
						body = `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>123456789012</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`
						contentType = "text/xml"
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {contentType}},
						Body:       io.NopCloser(strings.NewReader(body)),
					}, nil
				}),
			}
			var err error
			switch service {
			case "ssm":
				t.Setenv("LAMBDAFY_TEST_SSM", "*ssm:/new-sana/api-key")
				loader := starenv.NewLoader()
				loader.Register("ssm", starenv.NewAWSParameterStoreWithConfig(cfg))
				if errs := loader.Load(); len(errs) > 0 {
					t.Fatalf("load SSM environment variable: %v", errs)
				}
				if got := os.Getenv("LAMBDAFY_TEST_SSM"); got != "test-value" {
					t.Fatalf("loaded value = %q, want test-value", got)
				}
			case "sqs":
				_, err = sqs.NewFromConfig(cfg).SendMessage(context.Background(), &sqs.SendMessageInput{
					QueueUrl:    aws.String("https://sqs.ap-southeast-2.amazonaws.com/123456789012/test"),
					MessageBody: aws.String("test"),
				})
			case "sts":
				_, err = sts.NewFromConfig(cfg).GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{})
			}
			if err != nil {
				t.Fatal(err)
			}
			if requests != 1 {
				t.Fatalf("sent %d requests, want 1", requests)
			}
		})
	}
}
