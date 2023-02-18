package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
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
			version, err := resolveVersion(fnName, args[1])
			if err != nil {
				return fmt.Errorf("failed to resolve version '%s': %s", args[1], err)
			}

			fnURL, err := deploy(fnName, version, prime)
			if err != nil {
				return err
			}
			return formatOutput(map[string]string{
				"name":    fnName,
				"version": strconv.Itoa(version),
				"url":     fnURL,
			})
		},
	}
	deployCmd.Flags().IntVar(&prime, "prime", 1, "prime the function by sending it concurrent requests")
}

func init() {
	var yes bool
	undeployCmd = &cobra.Command{
		Use:   "undeploy function-name",
		Short: "Remove deployment and make function inaccessible",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fnName := args[0]
			if !yes {
				return fmt.Errorf("must pass --yes to actually undeploy the '%s' function", fnName)
			}
			if err := undeploy(fnName); err != nil {
				return err
			}
			return nil
		},
	}
	undeployCmd.Flags().BoolVar(&yes, "yes", false, "Actually undeploy the function")
}

func prepareDeploy(ctx context.Context, lambdaCl *lambda.Client, fnName string, version int, alias string) (string, error) {

	var err error
	verStr := strconv.Itoa(version)

	// Create or update alias

	if err := retryOnResourceConflict(ctx, func() error {
		_, err := lambdaCl.CreateAlias(ctx, &lambda.CreateAliasInput{
			FunctionName:    &fnName,
			FunctionVersion: &verStr,
			Name:            &alias,
		})
		return err
	}); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return "", fmt.Errorf("failed to create function alias '%s': %s", alias, err)
		}
		if err := retryOnResourceConflict(ctx, func() error {
			_, err := lambdaCl.UpdateAlias(ctx, &lambda.UpdateAliasInput{
				FunctionName:    &fnName,
				FunctionVersion: &verStr,
				Name:            &alias,
			})
			return err
		}); err != nil {
			return "", fmt.Errorf("failed to update function alias 'active': %s", err)
		}
	}

	// Check if CORS is enabled

	gfo, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: &fnName,
		Qualifier:    &alias,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get function '%s' alias '%s': %s", fnName, alias, err)
	}
	env := gfo.Configuration.Environment
	var cors lambdatypes.Cors
	if env != nil && env.Variables[specInEnvPrefix+"CORS"] == "true" {
		cors.AllowOrigins = []string{"*"}
	}

	// Create or update function URL

	var fnURL string
	var cfuc *lambda.CreateFunctionUrlConfigOutput
	if err := retryOnResourceConflict(ctx, func() error {
		cfuc, err = lambdaCl.CreateFunctionUrlConfig(ctx, &lambda.CreateFunctionUrlConfigInput{
			AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
			FunctionName: &fnName,
			Qualifier:    &alias,
			Cors:         &cors,
		})
		return err
	}); err != nil {
		if !strings.Contains(err.Error(), "exists for this") {
			return "", fmt.Errorf("failed to create function URL for alias '%s': %s", alias, err)
		}
		if err := retryOnResourceConflict(ctx, func() error {
			ufuc, err := lambdaCl.UpdateFunctionUrlConfig(ctx, &lambda.UpdateFunctionUrlConfigInput{
				AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
				FunctionName: &fnName,
				Qualifier:    &alias,
				Cors:         &cors,
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

	if err := retryOnResourceConflict(ctx, func() error {
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

// enableSQSTrigggers enables or disables all SQS triggers for the given function alias.
func enableSQSTriggers(ctx context.Context, lambdaCl *lambda.Client, fnName string, version int, enable bool) error {
	lst := []lambdatypes.EventSourceMappingConfiguration{}
	ems := lambda.NewListEventSourceMappingsPaginator(lambdaCl, &lambda.ListEventSourceMappingsInput{
		FunctionName: aws.String(fmt.Sprintf("%s:%d", fnName, version)),
	})
	for ems.HasMorePages() {
		es, err := ems.NextPage(ctx)
		if err != nil {
			return err
		}
		lst = append(lst, es.EventSourceMappings...)
	}

	for _, em := range lst {
		if !strings.HasPrefix(*em.EventSourceArn, "arn:aws:sqs:") {
			continue
		}
		if err := retryOnResourceConflict(ctx, func() error {
			_, err := lambdaCl.UpdateEventSourceMapping(ctx, &lambda.UpdateEventSourceMappingInput{
				UUID:    em.UUID,
				Enabled: &enable,
			})
			return err
		}); err != nil {
			return err
		}
	}

	// Wait for all triggers to be enabled/disabled.

	for {
		allAtDesiredState := true
		for _, em := range lst {
			s, err := lambdaCl.GetEventSourceMapping(ctx, &lambda.GetEventSourceMappingInput{
				UUID: em.UUID,
			})
			if err != nil {
				return err
			}
			if enable && *s.State != "Enabled" || !enable && *s.State != "Disabled" {
				allAtDesiredState = false
				break
			}
		}
		if allAtDesiredState {
			break
		}
		time.Sleep(1 * time.Second)
	}

	return nil
}

// publish publishes the lambda function to AWS and returns the function URL.
func deploy(fnName string, version int, primeCount int) (string, error) {
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

	ctxTo, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	preactiveFnURL, err := prepareDeploy(ctxTo, lambdaCl, fnName, version, preactiveAlias)
	if err != nil {
		return "", err
	}

	log.Print("waiting for function to return non 5xx")

	errInst := fmt.Sprintf("Check staging endpoint '%s' and review logs by running 'lambdafy logs -s 15m -v %d %s'", preactiveFnURL, version, fnName)

	// Run with 1 concurrency first to ensure function doesn't make debugging hard
	// by producing too many log entries.
	if err := prime(ctx, preactiveFnURL, 1); err != nil {
		return "", fmt.Errorf("function failed to return non 5xx - aborting deploy: %s\n\n%s", err, errInst)
	}

	if err := prime(ctx, preactiveFnURL, primeCount); err != nil {
		return "", fmt.Errorf("function failed to return non 5xx - aborting deploy: %s\n\n%s", err, errInst)
	}

	log.Printf("staging success")

	log.Printf("transitioning SQS triggers to the new version")

	// We first enable the SQS triggers for the new version to ensure we are not
	// left without any message receivers should something fail here.

	sqsCtx, sqsCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer sqsCancel()
	if err := enableSQSTriggers(sqsCtx, lambdaCl, fnName, version, true); err != nil {
		return "", fmt.Errorf("failed to enable SQS triggers: %s", err)
	}

	numVer, err := resolveVersion(fnName, activeAlias)
	if err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return "", fmt.Errorf("failed to resolve version for alias '%s': %s", activeAlias, err)
		}
	} else {
		if err := enableSQSTriggers(sqsCtx, lambdaCl, fnName, numVer, false); err != nil {
			return "", fmt.Errorf("failed to disable SQS triggers: %s", err)
		}
	}

	log.Printf("(re-)creating cron triggers for the new version")

	schedCl := scheduler.NewFromConfig(acfg)
	schedGroupName := fmt.Sprintf("lambdafy-%s", fnName)
	if _, err := schedCl.DeleteScheduleGroup(ctx, &scheduler.DeleteScheduleGroupInput{
		Name: &schedGroupName,
	}); err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return "", fmt.Errorf("failed to delete schedule group: %s", err)
		}
	}

	// Load env vars from function config and extract cron defs from it.

	fnCfg, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: &fnName,
		Qualifier:    aws.String(strconv.Itoa(version)),
	})
	if err != nil {
		return "", fmt.Errorf("failed to get function config: %s", err)
	}
	crons := make(map[string]string)
	for k, v := range fnCfg.Configuration.Environment.Variables {
		if !strings.HasPrefix(k, specInEnvCronPrefix) {
			continue
		}
		crons[k[len(specInEnvCronPrefix):]] = v
	}

	if len(crons) > 0 {
		// We need to retry because DeleteScheduleGroup call above takes time to
		// complete.
		ctxTo, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := retry(ctxTo, func() error {
			_, err := schedCl.CreateScheduleGroup(ctxTo, &scheduler.CreateScheduleGroupInput{
				Name: &schedGroupName,
			})
			return err
		}, "ConflictException"); err != nil {
			return "", fmt.Errorf("failed to create schedule group: %s", err)
		}
		for k, v := range crons {
			// payload is used by the proxy to extract the name of the cron and pass
			// it onto the app.
			payload, _ := json.Marshal(map[string]string{
				"cron": k,
			})
			if _, err := schedCl.CreateSchedule(ctx, &scheduler.CreateScheduleInput{
				Name:               aws.String(fmt.Sprintf("lambdafy-%s-%s", fnName, k)),
				GroupName:          &schedGroupName,
				ScheduleExpression: aws.String(fmt.Sprintf("cron(%s)", v)),
				Target: &schedulertypes.Target{
					Arn:     fnCfg.Configuration.FunctionArn,
					RoleArn: fnCfg.Configuration.Role,
					Input:   aws.String(string(payload)),
				},
				FlexibleTimeWindow: &schedulertypes.FlexibleTimeWindow{
					Mode: schedulertypes.FlexibleTimeWindowModeOff,
				},
			}); err != nil {
				return "", fmt.Errorf("failed to create schedule: %s", err)
			}
		}
	}

	log.Printf("deploying to active endpoint")

	ctxTo, cancel = context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	activeFnURL, err := prepareDeploy(ctxTo, lambdaCl, fnName, version, activeAlias)
	if err != nil {
		return "", err
	}

	// Wait for function to stabilize

	return activeFnURL, nil
}

func undeploy(fnName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	log.Print("disabling SQS triggers")

	numVer, err := resolveVersion(fnName, activeAlias)
	if err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return fmt.Errorf("failed to resolve version for alias '%s': %s", activeAlias, err)
		}
	} else {
		if err := enableSQSTriggers(ctx, lambdaCl, fnName, numVer, false); err != nil {
			return fmt.Errorf("failed to disable SQS triggers: %s", err)
		}
		if err := waitOnFunc(ctx, lambdaCl, fnName, activeAlias); err != nil {
			return err
		}
	}

	log.Print("deleting the function url endpoint")

	if err := retryOnResourceConflict(ctx, func() error {
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

// prime primes the function by sending requests to it.
func prime(ctx context.Context, url string, num int) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
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
