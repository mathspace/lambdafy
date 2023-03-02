package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	sqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

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
