package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/urfave/cli/v2"

	"github.com/mathspace/lambdafy/fnspec"
)

// publish publishes the lambda function to AWS.
func publish(specReader io.Reader) error {
	spec, err := fnspec.Load(specReader)
	if err != nil {
		return fmt.Errorf("failed to load function spec: %s", err)
	}

	ctx := context.Background()

	// Setup clients

	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}

	// Is the region allowed by spec?

	stsCl := sts.NewFromConfig(acfg)
	cid, err := stsCl.GetCallerIdentity(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get aws account number: %s", err)
	}
	if !spec.IsAccountRegionAllowed(*cid.Account, acfg.Region) {
		return fmt.Errorf("aws account and/or region is not allowed by spec")
	}

	// Prepare to create/update lambda function

	if len(spec.Entrypoint) > 0 {
		spec.Entrypoint = append([]string{"/lambdafy-proxy"}, spec.Entrypoint...)
	}

	fnName := "lambdafy-" + spec.Name
	roleName := c.String("default-role")
	if spec.Role != "" {
		roleName = spec.Role
	}

	iamCl := iam.NewFromConfig(acfg)
	role, err := iamCl.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err != nil {
		return fmt.Errorf("failed to get lambdafy role: %s", err)
	}

	lambdaCl := lambda.NewFromConfig(acfg)
	if _, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(fnName),
	}); err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return fmt.Errorf("failed to get lambda function: %T", err)
		}

		log.Printf("- creating new lambda function '%s'", fnName)

		_, err := lambdaCl.CreateFunction(ctx, &lambda.CreateFunctionInput{
			FunctionName:  aws.String(fnName),
			Description:   aws.String(spec.Description),
			Role:          role.Role.Arn,
			Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureX8664},
			Environment:   &lambdatypes.Environment{Variables: spec.Env},
			Code: &lambdatypes.FunctionCode{
				ImageUri: aws.String(repoImage),
			},
			ImageConfig: &lambdatypes.ImageConfig{
				EntryPoint:       spec.Entrypoint,
				Command:          spec.Command,
				WorkingDirectory: spec.WorkDir,
			},
			MemorySize:  spec.Memory,
			PackageType: lambdatypes.PackageTypeImage,
			Publish:     true,
			Tags: map[string]string{
				"Name": fnName,
			},
			Timeout: spec.Timeout,
		})
		if err != nil {
			return fmt.Errorf("failed to create lambda function: %s", err)
		}

	} else {
		log.Printf("- updating existing lambda function '%s'", fnName)

		// Run the update in a loop ignroing resource conflict errors which occur
		// for a while after a recent update to the function.

		for {
			if _, err := lambdaCl.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
				FunctionName:  aws.String(fnName),
				Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureX8664},
				ImageUri:      aws.String(repoImage),
				Publish:       true,
			}); err != nil {
				if strings.Contains(err.Error(), "ResourceConflictException") {
					time.Sleep(time.Second)
					continue
				}
				return fmt.Errorf("failed to update lambda function code: %s", err)
			}
			break
		}

		// Run the update in a loop ignroing resource conflict errors which occur
		// for a while after a recent update to the function.

		for {
			if _, err := lambdaCl.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
				FunctionName: aws.String(fnName),
				Description:  aws.String(spec.Description),
				Role:         role.Role.Arn,
				Environment:  &lambdatypes.Environment{Variables: spec.Env},
				ImageConfig: &lambdatypes.ImageConfig{
					EntryPoint:       spec.Entrypoint,
					Command:          spec.Command,
					WorkingDirectory: spec.WorkDir,
				},
				MemorySize: spec.Memory,
				Timeout:    spec.Timeout,
			}); err != nil {
				if strings.Contains(err.Error(), "ResourceConflictException") {
					time.Sleep(time.Second)
					continue
				}
				return fmt.Errorf("failed to update lambda function config: %s", err)
			}
			break
		}
	}

	if _, err := lambdaCl.AddPermission(ctx, &lambda.AddPermissionInput{
		StatementId:         aws.String("AllowPublicAccess"),
		Action:              aws.String("lambda:InvokeFunctionUrl"),
		FunctionName:        aws.String(fnName),
		Principal:           aws.String("*"),
		FunctionUrlAuthType: lambdatypes.FunctionUrlAuthTypeNone,
	}); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("failed to add public access permission: %s", err)
		}
	}

	if spec.ReservedConcurrency == nil {
		if _, err := lambdaCl.DeleteFunctionConcurrency(ctx, &lambda.DeleteFunctionConcurrencyInput{
			FunctionName: aws.String(fnName),
		}); err != nil {
			return fmt.Errorf("failed to remove reserved concurrency: %s", err)
		}
	} else {
		if _, err := lambdaCl.PutFunctionConcurrency(ctx, &lambda.PutFunctionConcurrencyInput{
			FunctionName:                 aws.String(fnName),
			ReservedConcurrentExecutions: spec.ReservedConcurrency,
		}); err != nil {
			return fmt.Errorf("failed to set reserved concurrency: %s", err)
		}
	}

	// Create/update lambda function URL

	fOut, err := lambdaCl.GetFunctionUrlConfig(ctx, &lambda.GetFunctionUrlConfigInput{
		FunctionName: aws.String(fnName),
	})
	var fnUrl string
	if err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return fmt.Errorf("failed to get lambda function url config: %s", err)
		}

		fOut, err := lambdaCl.CreateFunctionUrlConfig(ctx, &lambda.CreateFunctionUrlConfigInput{
			AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
			FunctionName: aws.String(fnName),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create function url: %s", err)
		}
		fnUrl = *fOut.FunctionUrl
	} else {
		fnUrl = *fOut.FunctionUrl
	}

	// Wait until function is in active state

activeWait:
	for {
		fOut, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
			FunctionName: aws.String(fnName),
		})
		if err != nil {
			return fmt.Errorf("failed poll function state: %s", err)
		}
		switch s := fOut.Configuration.State; s {
		case lambdatypes.StateActive:
			break activeWait
		case lambdatypes.StatePending:
			time.Sleep(2 * time.Second)
			continue
		default:
			return fmt.Errorf("invalid state while polling: %s", s)
		}
	}

	log.Print("- deploy complete")
	return map[string]string{
		"url":      fnUrl,
		"image":    repoImage,
		"function": fnName,
	}, nil
}

func deleteApp(c *cli.Context) error {

	ctx := context.Background()

	log.Print("- setting up")

	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	allowed, err := isAccountRegionAllowed(ctx, acfg)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("aws account and/or region is not allowed by spec")
	}

	lambdaCl := lambda.NewFromConfig(acfg)
	fnName := "lambdafy-" + spec.Name

	log.Printf("- deleting lambda function '%s'", fnName)

	if _, err := lambdaCl.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
		FunctionName: aws.String(fnName),
	}); err != nil {
		if strings.Contains(err.Error(), "ResourceNotFoundException") {
			log.Printf("- no lambda function named '%s' - skipping", fnName)
		} else {
			return fmt.Errorf("failed to delete lambda function: %s", err)
		}
	}

	log.Print("- done")

	return nil
}
