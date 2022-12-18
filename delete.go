package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/spf13/cobra"
)

var deleteCmd *cobra.Command

func init() {
	var yes bool
	deleteCmd = &cobra.Command{
		Use:   "delete function-name",
		Short: "delete the function",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if !yes {
				return errors.New("must pass --yes to actually delete the function")
			}
			return deleteFunction(args[0])
		},
	}
	deleteCmd.Flags().BoolVarP(&yes, "yes", "y", false, "Actually delete the function")
}

func deleteFunction(name string) error {
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	if err := retryOnResourceConflict(func() error {
		_, err := lambdaCl.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
			FunctionName: &name,
		})
		return err
	}); err != nil && !strings.Contains(err.Error(), "404") {
		return err
	}

	return nil
}
