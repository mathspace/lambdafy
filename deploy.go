package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// publish publishes the lambda function to AWS and returns the function URL.
func deploy(fnName string, version string) (string, error) {
	ctx := context.Background()

	// Setup clients

	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	// Create or update "active" alias

	if err := retryOnResourceConflict(func() error {
		_, err := lambdaCl.CreateAlias(ctx, &lambda.CreateAliasInput{
			FunctionName:    &fnName,
			FunctionVersion: &version,
			Name:            aws.String("active"),
		})
		return err
	}); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return "", fmt.Errorf("failed to create function alias 'active': %s", err)
		}
		if err := retryOnResourceConflict(func() error {
			_, err := lambdaCl.UpdateAlias(ctx, &lambda.UpdateAliasInput{
				FunctionName:    &fnName,
				FunctionVersion: &version,
				Name:            aws.String("active"),
			})
			return err
		}); err != nil {
			return "", fmt.Errorf("failed to update function alias 'active': %s", err)
		}
	}

	// Create or update function URL

	var fnURL string
	var cfuc *lambda.CreateFunctionUrlConfigOutput
	if err := retryOnResourceConflict(func() error {
		cfuc, err = lambdaCl.CreateFunctionUrlConfig(ctx, &lambda.CreateFunctionUrlConfigInput{
			AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
			FunctionName: aws.String(fnName),
			Qualifier:    aws.String("active"),
		})
		return err
	}); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return "", fmt.Errorf("failed to create function URL for alias 'active': %s", err)
		}
		if err := retryOnResourceConflict(func() error {
			ufuc, err := lambdaCl.UpdateFunctionUrlConfig(ctx, &lambda.UpdateFunctionUrlConfigInput{
				AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
				FunctionName: aws.String(fnName),
				Qualifier:    aws.String("active"),
			})
			fnURL = *ufuc.FunctionUrl
			return err
		}); err != nil {
			return "", fmt.Errorf("failed to update function URL for alias 'active': %s", err)
		}
	} else {
		fnURL = *cfuc.FunctionUrl
	}

	// Add public access permission

	if err := retryOnResourceConflict(func() error {
		_, err := lambdaCl.AddPermission(ctx, &lambda.AddPermissionInput{
			StatementId:         aws.String("AllowPublicAccess"),
			Action:              aws.String("lambda:InvokeFunctionUrl"),
			FunctionName:        &fnName,
			Principal:           aws.String("*"),
			Qualifier:           aws.String("active"),
			FunctionUrlAuthType: lambdatypes.FunctionUrlAuthTypeNone,
		})
		return err
	}); err != nil && !strings.Contains(err.Error(), "already exists") {
		return "", fmt.Errorf("failed to add public access permission to 'active' alias URL: %s", err)
	}

	// Wait for function to stabilize

	return fnURL, waitOnFunc(ctx, lambdaCl, fnName)
}
