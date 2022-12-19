package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/spf13/cobra"
)

const aliasPatStr = `^[a-zA-Z_][a-zA-Z0-9_-]*$`

var aliasPat = regexp.MustCompile(aliasPatStr)

var aliasCmd = &cobra.Command{
	Use:   "alias function-name version alias-name",
	Short: "Create an alias for a function at a specific version",
	Args:  cobra.ExactArgs(3),
	RunE: func(c *cobra.Command, args []string) error {
		fnName := args[0]
		version := args[1]
		aliasName := args[2]
		return alias(fnName, version, aliasName)
	},
}

var unaliasCmd = &cobra.Command{
	Use:   "unalias function-name alias-name",
	Short: "Deletes an existing function alias",
	Args:  cobra.ExactArgs(2),
	RunE: func(c *cobra.Command, args []string) error {
		return unalias(args[0], args[1])
	},
}

// alias creates an alias for a function at a specific version.
func alias(fnName string, version string, aliasName string) error {
	if aliasPat.MatchString(aliasName) {
		return fmt.Errorf("invalid alias name: '%s' - must match '%s'", aliasName, aliasPatStr)
	}
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	verInt, err := resolveVersion(fnName, version)

	if _, err = lambdaCl.CreateAlias(ctx, &lambda.CreateAliasInput{
		FunctionName:    &fnName,
		FunctionVersion: aws.String(strconv.Itoa(verInt)),
		Name:            &aliasName,
	}); err != nil {
		return fmt.Errorf("failed to create alias: %s", err)
	}

	return nil
}

func unalias(fnName, aliasName string) error {
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	if _, err = lambdaCl.DeleteAlias(ctx, &lambda.DeleteAliasInput{
		FunctionName: &fnName,
		Name:         &aliasName,
	}); err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil
		}
		return fmt.Errorf("failed to delete alias: %s", err)
	}
	return nil
}
