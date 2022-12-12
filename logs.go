package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

type fnLogs struct {
	// Token to pass to logs() to get more recent logs.
	afterToken string
	// Log lines in chronological order.
	lines []string
}

// logs returns the logs for a function at the specified version.
// If version is empty, last active version of the function is assumed.
// afterToken is a token to pass to get more recent logs.
// This log retriever is super primitive, thanks to the complexities of AWS.
// It basically pulls the last 60s of the logs.
func logs(fnName string, version string, since time.Time, afterToken string) (fnLogs, error) {
	lgs := fnLogs{}
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return lgs, fmt.Errorf("failed to load aws config: %s", err)
	}
	logsCl := cloudwatchlogs.NewFromConfig(acfg)

	logGroupName := aws.String(fmt.Sprintf("/aws/lambda/%s", fnName))

	// This is a hack to get the logs for a specific version. To cater for
	// logstreams that start just before a new date or run for multiple days, we
	// look at logstreams a few days into the past first.
	const maxDays = 3
	prefixDate := since.UTC().AddDate(0, 0, -(maxDays - 1))

	for i := 0; i < maxDays; i++ {
		streamPrefix := fmt.Sprintf("%s/[%s]", prefixDate.Format("2006/01/02"), version)
		pgr := cloudwatchlogs.NewFilterLogEventsPaginator(logsCl, &cloudwatchlogs.FilterLogEventsInput{
			LogGroupName:        logGroupName,
			StartTime:           aws.Int64(since.UnixMilli()),
			LogStreamNamePrefix: aws.String(streamPrefix),
			Limit:               aws.Int32(10000),
		})
		for pgr.HasMorePages() {
			ents, err := pgr.NextPage(ctx)
			if err != nil {
				return lgs, fmt.Errorf("failed to get log events: %s", err)
			}
			for _, e := range ents.Events {
				if afterToken == "" {
					lgs.lines = append(lgs.lines, strings.TrimSuffix(*e.Message, "\n"))
				} else {
					if *e.EventId == afterToken {
						afterToken = ""
					}
				}
				lgs.afterToken = *e.EventId
			}
		}
		prefixDate = prefixDate.AddDate(0, 0, 1)
	}

	return lgs, nil

}
