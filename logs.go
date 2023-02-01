package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/spf13/cobra"
)

var logsCmd *cobra.Command

func init() {
	var ver string
	var sinceDur time.Duration
	var tail bool
	logsCmd = &cobra.Command{
		Use:     "logs function-name",
		Aliases: []string{"log"},
		Short:   "Print out most recent logs for the function",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			since := time.Now().Add(-sinceDur)
			fnName := args[0]
			ver, err := resolveVersion(fnName, ver)
			if err != nil {
				return fmt.Errorf("failed to resolve version: %s", err)
			}

			log.Printf("printing logs for version %d", ver)

			var afterToken string
			for {
				lgs, err := logs(fnName, ver, since, afterToken)
				if err != nil {
					return err
				}
				for _, l := range lgs.lines {
					fmt.Println(l)
				}
				if !tail {
					return nil
				}
				afterToken = lgs.afterToken
				since = time.Now().Add(-30 * time.Second)
				time.Sleep(2 * time.Second)
			}
		},
	}
	addVersionFlag(logsCmd.Flags(), &ver)
	logsCmd.Flags().BoolVarP(&tail, "tail", "t", false, "wait for new logs and print them as they come in")
	logsCmd.Flags().DurationVarP(&sinceDur, "since", "s", time.Minute, "only print logs since this length of time ago")
}

type fnLogs struct {
	// Token to pass to logs() to get more recent logs.
	afterToken string
	// Log lines in chronological order.
	lines []string
}

// logs returns the logs for a function at the specified version.
// afterToken is a token to pass to get more recent logs.
// This log retriever is super primitive, thanks to the complexities of AWS.
func logs(fnName string, version int, since time.Time, afterToken string) (fnLogs, error) {
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
	const maxDays = 2
	prefixDate := since.UTC().AddDate(0, 0, -(maxDays - 1))
	now := time.Now()

	for prefixDate.Before(now) {
		streamPrefix := fmt.Sprintf("%s/[%d]", prefixDate.Format("2006/01/02"), version)
		pgr := cloudwatchlogs.NewFilterLogEventsPaginator(logsCl, &cloudwatchlogs.FilterLogEventsInput{
			LogGroupName:        logGroupName,
			StartTime:           aws.Int64(since.UnixMilli()),
			LogStreamNamePrefix: aws.String(streamPrefix),
			Limit:               aws.Int32(10000),
		})
		for pgr.HasMorePages() {
			ents, err := pgr.NextPage(ctx)
			if err != nil {
				if !strings.Contains(err.Error(), "ResourceNotFoundException") {
					return lgs, fmt.Errorf("failed to get log events: %s", err)
				}
				break
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
