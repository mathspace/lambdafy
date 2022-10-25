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

	"github.com/mathspace/lambdafy/fnspec"
)

// publishResult holds the results of a publish operation.
type publishResult struct {
	arn     string
	version string
}

// publish publishes the lambda function to AWS.
func publish(specReader io.Reader) (res publishResult, err error) {
	spec, err := fnspec.Load(specReader)
	if err != nil {
		return res, fmt.Errorf("failed to load function spec: %s", err)
	}

	ctx := context.Background()

	// Setup clients

	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to load aws config: %s", err)
	}

	// Is the region allowed by spec?

	stsCl := sts.NewFromConfig(acfg)
	cid, err := stsCl.GetCallerIdentity(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("failed to get aws account number: %s", err)
	}
	if !spec.IsAccountRegionAllowed(*cid.Account, acfg.Region) {
		return res, fmt.Errorf("aws account and/or region is not allowed by spec")
	}

	// Prepare to create/update lambda function

	if len(spec.Entrypoint) > 0 && spec.Entrypoint[0] != "/lambdafy-proxy" {
		spec.Entrypoint = append([]string{"/lambdafy-proxy"}, spec.Entrypoint...)
	}

	iamCl := iam.NewFromConfig(acfg)
	role, err := iamCl.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(spec.Role)})
	if err != nil {
		return res, fmt.Errorf("failed to lookup role '%s': %s", spec.Role, err)
	}

	tags := make(map[string]string, len(spec.Tags))
	tags["Name"] = spec.Name
	for k, v := range spec.Tags {
		tags[k] = v
	}

	var vpc *lambdatypes.VpcConfig
	if len(spec.VPCSubnetIds) > 0 || len(spec.VPCSecurityGroupIds) > 0 {
		vpc = &lambdatypes.VpcConfig{
			SubnetIds:        spec.VPCSubnetIds,
			SecurityGroupIds: spec.VPCSecurityGroupIds,
		}
	}

	fsConfig := make([]lambdatypes.FileSystemConfig, len(spec.EFSMounts))
	for i, m := range spec.EFSMounts {
		fsConfig[i] = lambdatypes.FileSystemConfig{
			Arn:            aws.String(m.ARN),
			LocalMountPath: aws.String(m.Path),
		}
	}

	lambdaCl := lambda.NewFromConfig(acfg)
	fn, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(spec.Name),
	})
	if err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return res, fmt.Errorf("failed to lookup function '%s': %s", spec.Name, err)
		}

		log.Printf("- creating new function '%s'", spec.Name)

		r, err := lambdaCl.CreateFunction(ctx, &lambda.CreateFunctionInput{
			FunctionName:  aws.String(spec.Name),
			Description:   aws.String(spec.Description),
			Role:          role.Role.Arn,
			Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureX8664},
			Environment:   &lambdatypes.Environment{Variables: spec.Env},
			Code: &lambdatypes.FunctionCode{
				ImageUri: aws.String(spec.Image),
			},
			ImageConfig: &lambdatypes.ImageConfig{
				EntryPoint:       spec.Entrypoint,
				Command:          spec.Command,
				WorkingDirectory: spec.WorkDir,
			},
			FileSystemConfigs: fsConfig,
			MemorySize:        spec.Memory,
			PackageType:       lambdatypes.PackageTypeImage,
			Publish:           true,
			Tags:              tags,
			Timeout:           spec.Timeout,
			VpcConfig:         vpc,
		})
		if err != nil {
			return res, fmt.Errorf("failed to create function: %s", err)
		}
		res.arn = *r.FunctionArn
		res.version = *r.Version

	} else {
		log.Printf("- updating existing function '%s'", spec.Name)

		// Run the update in a loop ignoring resource conflict errors which occur
		// for a while after a recent update to the function.

		for {
			r, err := lambdaCl.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
				FunctionName: aws.String(spec.Name),
				Description:  aws.String(spec.Description),
				Role:         role.Role.Arn,
				Environment:  &lambdatypes.Environment{Variables: spec.Env},
				ImageConfig: &lambdatypes.ImageConfig{
					EntryPoint:       spec.Entrypoint,
					Command:          spec.Command,
					WorkingDirectory: spec.WorkDir,
				},
				FileSystemConfigs: fsConfig,
				MemorySize:        spec.Memory,
				Timeout:           spec.Timeout,
				VpcConfig:         vpc,
			})
			if err != nil {
				if strings.Contains(err.Error(), "ResourceConflictException") {
					time.Sleep(time.Second)
					continue
				}
				return res, fmt.Errorf("failed to update function config: %s", err)
			}
			res.arn = *r.FunctionArn
			res.version = *r.Version
			break
		}

		// Run the update in a loop ignoring resource conflict errors which occur
		// for a while after a recent update to the function.

		for {
			if _, err := lambdaCl.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
				FunctionName:  aws.String(spec.Name),
				Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureX8664},
				ImageUri:      aws.String(spec.Image),
				Publish:       true,
			}); err != nil {
				if strings.Contains(err.Error(), "ResourceConflictException") {
					time.Sleep(time.Second)
					continue
				}
				return res, fmt.Errorf("failed to update function code: %s", err)
			}
			break
		}

		// Re-tag the function

		if _, err := lambdaCl.TagResource(ctx, &lambda.TagResourceInput{
			Resource: fn.Configuration.FunctionArn,
			Tags:     tags,
		}); err != nil {
			return res, fmt.Errorf("failed to tag function: %s", err)
		}

		// Untag old tags

		oldTags := []string{}
		for k := range fn.Tags {
			if _, ok := tags[k]; !ok {
				oldTags = append(oldTags, k)
			}
		}

		if _, err := lambdaCl.UntagResource(ctx, &lambda.UntagResourceInput{
			Resource: fn.Configuration.FunctionArn,
			TagKeys:  oldTags,
		}); err != nil {
			return res, fmt.Errorf("failed to old tags: %s", err)
		}

	}

	// Wait until function is in active state

activeWait:
	for {
		fOut, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
			FunctionName: aws.String(spec.Name),
		})
		if err != nil {
			return res, fmt.Errorf("failed poll function state: %s", err)
		}
		switch s := fOut.Configuration.State; s {
		case lambdatypes.StateActive:
			break activeWait
		case lambdatypes.StatePending:
			time.Sleep(2 * time.Second)
			continue
		default:
			return res, fmt.Errorf("invalid state while polling: %s", s)
		}
	}

	return res, nil
}
