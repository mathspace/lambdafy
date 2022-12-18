package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/spf13/cobra"
)

const activeAlias = "lambdafy-active"
const preactiveAlias = "lambdafy-preactive"

var (
	deployCmd   *cobra.Command
	undeployCmd *cobra.Command
)

func init() {
	var prime int
	deployCmd = &cobra.Command{
		Use:   "deploy function-name version",
		Short: "Deploy a specific version of a function to a public URL",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			if prime < 1 || prime > 100 {
				return fmt.Errorf("--prime must be between 1 and 100")
			}
			fnName := args[0]
			version := args[1]

			fnURL, err := deploy(fnName, version, prime)
			if err != nil {
				return err
			}
			log.Printf("deployed function '%s' version '%s' to '%s'", fnName, version, fnURL)
			return nil
		},
	}
	deployCmd.Flags().Int("prime", 1, "prime the function by sending it concurrent requests")
}

func init() {
	var yes bool
	undeployCmd = &cobra.Command{
		Use:   "undeploy function-name",
		Short: "Remove deployment and make function inaccessible",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if !yes {
				return errors.New("must pass --yes to actually undeploy the function")
			}
			if err := undeploy(args[0]); err != nil {
				return err
			}
			return nil
		},
	}
	undeployCmd.Flags().BoolVar(&yes, "yes", false, "Actually undeploy the function")
}

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
func deploy(fnName string, version string, primeCount int) (string, error) {
	ctx := context.Background()

	// Setup clients

	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	// Prepare preactive deploy:
	// Once we ensure the function works, we will switch the active alias to point to this version.

	log.Printf("deploying to staging endpoint for testing")

	preactiveFnURL, err := prepareDeploy(ctx, lambdaCl, fnName, version, preactiveAlias)
	if err != nil {
		return "", err
	}

	// Loop until the function returns a [234]XX response.

	log.Print("waiting for function to return success")

	errInst := fmt.Sprintf("Check staging endpoint '%s' and review logs by running 'lambdafy logs -s 15 -v %s %s'", preactiveFnURL, version, fnName)

	// Run with 1 concurrency first to ensure function doesn't make debugging hard
	// by producing too many log entries.
	if err := prime(preactiveFnURL, 1); err != nil {
		return "", fmt.Errorf("function failed to return success - aborting deploy: %s\n\n%s", err, errInst)
	}

	if err := prime(preactiveFnURL, primeCount); err != nil {
		return "", fmt.Errorf("function failed to return success - aborting deploy: %s\n\n%s", err, errInst)
	}

	// Prepare active deploy.

	log.Printf("staging success - deploying to active endpoint")

	activeFnURL, err := prepareDeploy(ctx, lambdaCl, fnName, version, activeAlias)
	if err != nil {
		return "", err
	}

	// Wait for function to stabilize

	return activeFnURL, nil
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

func prime(url string, num int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	wg := sync.WaitGroup{}
	wg.Add(num)
	errCh := make(chan error, num)

	for i := 0; i < num; i++ {
		go func() {
			defer wg.Done()
			conseqSuccess := 0
			for {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				if err != nil {
					errCh <- fmt.Errorf("failed to create request: %s", err)
					return
				}
				resp, err := http.DefaultClient.Do(req)
				if err == context.Canceled || err == context.DeadlineExceeded {
					return
				}
				if err == nil {
					resp.Body.Close()
				}
				if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 500 {
					conseqSuccess = 0
					time.Sleep(500 * time.Millisecond)
					continue
				}
				conseqSuccess++
				if conseqSuccess == 3 {
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		cancel()
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timed out waiting for instances to warm up")
		}
	}
	return nil
}
