package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/spf13/cobra"
)

var deleteCmd *cobra.Command
var cleanupRolesCmd *cobra.Command

func init() {
	var yes bool
	deleteCmd = &cobra.Command{
		Use:   "delete function-name",
		Short: "Delete the function",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fnName := args[0]
			if !yes {
				return fmt.Errorf("must pass --yes to actually delete the '%s' function", fnName)
			}
			return deleteFunction(fnName)
		},
	}
	deleteCmd.Flags().BoolVarP(&yes, "yes", "y", false, "Actually delete the function")

	cleanupRolesCmd = &cobra.Command{
		Use:   "cleanup-roles",
		Short: "Cleans up unused generated roles",
		RunE: func(c *cobra.Command, args []string) error {
			return cleanupRoles()
		},
	}
}

func deleteFunction(name string) error {
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := retryOnResourceConflict(ctx, func() error {
		_, err := lambdaCl.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
			FunctionName: &name,
		})
		return err
	}); err != nil && !strings.Contains(err.Error(), "404") {
		return err
	}

	return nil
}

func cleanupRoles() error {
	return fmt.Errorf("not implemented")
}
