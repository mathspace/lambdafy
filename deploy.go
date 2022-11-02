package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

const activeAlias = "lambdafy-active"
const preactiveAlias = "lambdafy-preactive"

func prepareDeploy(ctx context.Context, lambdaCl *lambda.Client, fnName string, version string, alias string) (string, error) {

	var err error

	// Create or update alias

	if err := retryOnResourceConflict(func() error {
		_, err := lambdaCl.CreateAlias(ctx, &lambda.CreateAliasInput{
			FunctionName:    &fnName,
			FunctionVersion: &version,
			Name:            &alias,
		})
		return err
	}); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return "", fmt.Errorf("failed to create function alias '%s': %s", alias, err)
		}
		if err := retryOnResourceConflict(func() error {
			_, err := lambdaCl.UpdateAlias(ctx, &lambda.UpdateAliasInput{
				FunctionName:    &fnName,
				FunctionVersion: &version,
				Name:            &alias,
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
			FunctionName: &fnName,
			Qualifier:    &alias,
		})
		return err
	}); err != nil {
		if !strings.Contains(err.Error(), "exists for this") {
			return "", fmt.Errorf("failed to create function URL for alias '%s': %s", alias, err)
		}
		if err := retryOnResourceConflict(func() error {
			ufuc, err := lambdaCl.UpdateFunctionUrlConfig(ctx, &lambda.UpdateFunctionUrlConfigInput{
				AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
				FunctionName: &fnName,
				Qualifier:    &alias,
			})
			fnURL = *ufuc.FunctionUrl
			return err
		}); err != nil {
			return "", fmt.Errorf("failed to update function URL for alias '%s': %s", alias, err)
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
			Qualifier:           &alias,
			FunctionUrlAuthType: lambdatypes.FunctionUrlAuthTypeNone,
		})
		return err
	}); err != nil && !strings.Contains(err.Error(), "already exists") {
		return "", fmt.Errorf("failed to add public access permission to '%s' alias URL: %s", alias, err)
	}

	return fnURL, nil
}

// publish publishes the lambda function to AWS and returns the function URL.
func deploy(fnName string, version string) (string, error) {
	ctx := context.Background()

	// Setup clients

	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	// Prepare preactive deploy:
	// Once we ensure the function works, we will switch the active alias to point to this version.

	preactiveFnURL, err := prepareDeploy(ctx, lambdaCl, fnName, version, preactiveAlias)
	if err != nil {
		return "", err
	}

	// Loop until the funciton returns a 2XX/3XX response.

	var resp *http.Response
	for i := 0; i < 10; i++ {
		resp, err = http.Get(preactiveFnURL)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 400 {
			break
		}
		time.Sleep(time.Second * 6)
	}

	if err != nil {
		return "", fmt.Errorf("function check failed - aborting deploy: %s", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("function check failed - aborting deploy: last status = %s", resp.Status)
	}

	// Prepare active deploy.

	activeFnURL, err := prepareDeploy(ctx, lambdaCl, fnName, version, activeAlias)
	if err != nil {
		return "", err
	}

	// Wait for function to stabilize

	return activeFnURL, waitOnFunc(ctx, lambdaCl, fnName)
}

func undeploy(fnName string) error {
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	if err := retryOnResourceConflict(func() error {
		_, err := lambdaCl.DeleteAlias(ctx, &lambda.DeleteAliasInput{
			FunctionName: &fnName,
			Name:         aws.String(activeAlias),
		})
		return err
	}); err != nil && !strings.Contains(err.Error(), "404") {
		return err
	}

	return nil
}
