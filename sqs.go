package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/spf13/cobra"
)

var sqsCmd *cobra.Command

func init() {
	sqsCmd = &cobra.Command{
		Use:   "sqs",
		Short: "Manage SQS event sources",
		Long:  "Manage SQS event sources.\n\nThe function will receive one POST HTTP request to /_lambdafy/sqs path for every SQS message received with the HTTP request body being the body of the SQS message. A [23]xx response is considered success which causes the message to be deleted from the queue. A non [23]xx response is considered failure and will cause SQS to retry the message.",
	}

	var batchSize int
	addCmd := &cobra.Command{
		Use:   "add function-name sqs-arn",
		Short: "Add/update SQS event source",
		Long:  "Add/update SQS event source.\n\nThe function will receive up to max-batch-size number of concurrent requests.",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			return sqsAdd(args[0], args[1], batchSize)
		},
	}
	deployCmd.Flags().IntVarP(&batchSize, "max-batch-size", "b", 1, "maximum number of messages to process in a single batch")
	sqsCmd.AddCommand(addCmd)

	removeCmd := &cobra.Command{
		Use:     "remove function-name sqs-arn",
		Aliases: []string{"rm"},
		Short:   "Remove SQS event source",
		Long:    "Remove SQS event source.\n\nRemoves ALL SQS events sources with the given ARN from the function.",
		Args:    cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			return sqsRemove(args[0], args[1])
		},
	}
	sqsCmd.AddCommand(removeCmd)

	removeAllCmd := &cobra.Command{
		Use:   "remove-all function-name",
		Short: "Remove all SQS event source",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			return sqsRemove(args[0], "")
		},
	}
	sqsCmd.AddCommand(removeAllCmd)

	listCmd := &cobra.Command{
		Use:     "list function-name",
		Aliases: []string{"rm"},
		Short:   "List SQS event sources",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			out, err := sqsList(args[0])
			if err != nil {
				return err
			}
			formatOutput(out)
			return nil
		},
	}
	sqsCmd.AddCommand(listCmd)
}

type sqsEventSource struct {
	UUID      string `json:"-"`
	ARN       string `json:"arn"`
	BatchSize int    `json:"batchSize"`
}

// sqsList lists the SQS event sources for the function.
func sqsList(fnName string) ([]sqsEventSource, error) {
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	src := []sqsEventSource{}

	pag := lambda.NewListEventSourceMappingsPaginator(lambdaCl, &lambda.ListEventSourceMappingsInput{
		FunctionName: &fnName,
	})
	for pag.HasMorePages() {
		esm, err := pag.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list event source mappings: %s", err)
		}
		for _, m := range esm.EventSourceMappings {
			src = append(src, sqsEventSource{
				UUID:      *m.UUID,
				ARN:       *m.EventSourceArn,
				BatchSize: int(*m.BatchSize),
			})
		}
	}

	sort.Slice(src, func(i, j int) bool {
		return src[i].ARN < src[j].ARN
	})

	return src, nil
}

// sqsAdd adds a new SQS event source to the function.
func sqsAdd(fnName, arn string, batchSize int) error {
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	src, err := sqsList(fnName)
	if err != nil {
		return err
	}
	if len(src) > 1 {
		return fmt.Errorf("found multiple event source mappings for function %s and arn %s - must have zero or exactly one for the given SQS queue", fnName, arn)
	}

	if len(src) == 1 {
		if _, err = lambdaCl.UpdateEventSourceMapping(ctx, &lambda.UpdateEventSourceMappingInput{
			UUID:      &src[0].UUID,
			BatchSize: aws.Int32(int32(batchSize)),
		}); err != nil {
			return fmt.Errorf("failed to update event source mapping: %s", err)
		}
	} else {
		if _, err = lambdaCl.CreateEventSourceMapping(ctx, &lambda.CreateEventSourceMappingInput{
			EventSourceArn: &arn,
			FunctionName:   &fnName,
			BatchSize:      aws.Int32(int32(batchSize)),
		}); err != nil {
			return fmt.Errorf("failed to create event source mapping: %s", err)
		}
	}
	return nil
}

// sqsRemove removes all SQS event source with given ARN from the function. If
// ARN is empty, it will remove all SQS event sources.
func sqsRemove(fnName, arn string) error {
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	src, err := sqsList(fnName)
	if err != nil {
		return err
	}

	for _, m := range src {
		if arn != "" && m.ARN != arn {
			continue
		}
		if _, err = lambdaCl.DeleteEventSourceMapping(ctx, &lambda.DeleteEventSourceMappingInput{
			UUID: &m.UUID,
		}); err != nil {
			return fmt.Errorf("failed to delete event source mapping: %s", err)
		}
	}

	return nil
}
