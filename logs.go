package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/urfave/cli/v2"
)

var logsCmd = &cli.Command{
	Name:      "logs",
	Aliases:   []string{"log"},
	Usage:     "print out most recent logs for the function",
	ArgsUsage: "function-name",
	Flags: []cli.Flag{
		versionFlag,
		&cli.BoolFlag{
			Name:    "tail",
			Aliases: []string{"t"},
			Usage:   "wait for new logs and print them as they come in",
		},
		&cli.UintFlag{
			Name:    "since",
			Aliases: []string{"s"},
			Usage:   "only print logs since this many minutes ago",
			Value:   1,
		},
	},
	Action: func(c *cli.Context) error {
		if c.NArg() != 1 {
			return errors.New("must provide a function name as the only arg")
		}
		since := time.Now().Add(-time.Duration(c.Uint("since")) * time.Minute)
		fnName := c.Args().First()
		ver, err := resolveVersion(fnName, c.String("version"))
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
			if !c.Bool("tail") {
				return nil
			}
			afterToken = lgs.afterToken
			since = time.Now().Add(-30 * time.Second)
			time.Sleep(2 * time.Second)
		}
	},
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
// It basically pulls the last 60s of the logs.
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
	const maxDays = 3
	prefixDate := since.UTC().AddDate(0, 0, -(maxDays - 1))

	for i := 0; i < maxDays; i++ {
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
