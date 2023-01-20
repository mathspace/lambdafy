package main

import (
	"context"
	"fmt"

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
		Use:   "add sqs-arn",
		Short: "Add an SQS event source",
		Long:  "Add an SQS event source.\n\nThe function will receive up to max-batch-size number of concurrent requests.",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return sqsAdd(args[0], batchSize)
		},
	}
	deployCmd.Flags().IntVarP(&batchSize, "max-batch-size", "b", 1, "maximum number of messages to process in a single batch")

	sqsCmd.AddCommand(addCmd)
	removeCmd := &cobra.Command{
		Use:   "rm sqs-arn",
		Short: "Remove an SQS event source",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return sqsRemove(args[0])
		},
	}
	sqsCmd.AddCommand(removeCmd)
}

// sqsAdd adds a new SQS event source to the function.
func sqsAdd(arn string, batchSize int) error {
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)
	_ = lambdaCl
	// TODO
	return nil
}

// sqsRemove removes an SQS event source from the function.
func sqsRemove(arn string) error {
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)
	_ = lambdaCl

	return nil
	// TODO
}
