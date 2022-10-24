package main

/*

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	dockertypes "github.com/docker/docker/api/types"
	dockerclient "github.com/docker/docker/client"
	"github.com/urfave/cli/v2"
)

func isAccountRegionAllowed(ctx context.Context, acfg aws.Config) (bool, error) {
	stsCl := sts.NewFromConfig(acfg)
	cid, err := stsCl.GetCallerIdentity(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("failed to get aws account number: %s", err)
	}
	return spec.IsAccountRegionAllowed(*cid.Account, acfg.Region), nil
}

/*
// deployApp creates/updates necessary resources for running a lambda function
// with the given image and spec, exposed via a public URL.
func deployApp(c *cli.Context) (map[string]string, error) {
	if c.NArg() != 1 {
		return nil, fmt.Errorf("must provide a docker image name as first arg")
	}
	ctx := context.Background()

	log.Print("- setting up")

	// Setup clients

	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %s", err)
	}
	allowed, err := isAccountRegionAllowed(ctx, acfg)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, fmt.Errorf("aws account and/or region is not allowed by spec")
	}
	dc, err := dockerclient.NewClientWithOpts(
		dockerclient.WithAPIVersionNegotiation(),
		dockerclient.FromEnv,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get docker client: %s", err)
	}

	// Get the full ECR repo URL

	ecrCl := ecr.NewFromConfig(acfg)
	repOut, err := ecrCl.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
		RepositoryNames: []string{c.String("ecr-repo")},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get list of ecr repos: %s", err)
	}
	if len(repOut.Repositories) < 1 {
		return nil, fmt.Errorf("cannot find lambdafy ecr repo - is infra in place?")
	}
	repoUri := repOut.Repositories[0].RepositoryUri

	// Get the built image hash to use as tag on ECR

	img, _, err = dc.ImageInspectWithRaw(ctx, outImage)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect lambdafied image '%s': %s", outImage, err)
	}
	idSum := sha256.Sum256([]byte(img.ID))
	repoImage := fmt.Sprintf("%s:%s", *repoUri, hex.EncodeToString(idSum[:]))

	log.Printf("- tagging lambdafied image with '%s'", repoImage)

	if err := dc.ImageTag(ctx, outImage, repoImage); err != nil {
		return nil, fmt.Errorf("failed to tag lambdafied image: %s", err)
	}

	defer dc.ImageRemove(ctx, outImage, dockertypes.ImageRemoveOptions{
		Force: true,
	})

	log.Print("- pushing tagged image to ecr")

	// Log docker into ECR

	tokResp, err := ecrCl.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to get ecr auth token: %s", err)
	}
	if len(tokResp.AuthorizationData) < 1 {
		return nil, fmt.Errorf("missing ecr auth token")
	}
	authToken, err := base64.StdEncoding.DecodeString(*tokResp.AuthorizationData[0].AuthorizationToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ecr auth token: %s", err)
	}
	authTokenParts := strings.SplitN(string(authToken), ":", 2)
	if len(authTokenParts) != 2 {
		return nil, errors.New("invalid ecr auth token")
	}
	regEP := *tokResp.AuthorizationData[0].ProxyEndpoint

	authCfg := dockertypes.AuthConfig{
		Username:      authTokenParts[0],
		Password:      authTokenParts[1],
		ServerAddress: regEP,
	}
	authCfgBytes, _ := json.Marshal(authCfg)
	authCfgEncoded := base64.URLEncoding.EncodeToString(authCfgBytes)

	// Push the image to ECR

	rc, err := dc.ImagePush(ctx, repoImage, dockertypes.ImagePushOptions{
		RegistryAuth: authCfgEncoded,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to push tagged image '%s': %s", repoImage, err)
	}
	if err := processDockerResponse(rc); err != nil {
		rc.Close()
		return nil, fmt.Errorf("failed to push tagged image '%s': %s", repoImage, err)
	}
	rc.Close()

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
		return nil, fmt.Errorf("failed to get lambdafy role: %s", err)
	}

	lambdaCl := lambda.NewFromConfig(acfg)
	if _, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(fnName),
	}); err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return nil, fmt.Errorf("failed to get lambda function: %T", err)
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
			return nil, fmt.Errorf("failed to create lambda function: %s", err)
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
				return nil, fmt.Errorf("failed to update lambda function code: %s", err)
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
				return nil, fmt.Errorf("failed to update lambda function config: %s", err)
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
			return nil, fmt.Errorf("failed to add public access permission: %s", err)
		}
	}

	if spec.ReservedConcurrency == nil {
		if _, err := lambdaCl.DeleteFunctionConcurrency(ctx, &lambda.DeleteFunctionConcurrencyInput{
			FunctionName: aws.String(fnName),
		}); err != nil {
			return nil, fmt.Errorf("failed to remove reserved concurrency: %s", err)
		}
	} else {
		if _, err := lambdaCl.PutFunctionConcurrency(ctx, &lambda.PutFunctionConcurrencyInput{
			FunctionName:                 aws.String(fnName),
			ReservedConcurrentExecutions: spec.ReservedConcurrency,
		}); err != nil {
			return nil, fmt.Errorf("failed to set reserved concurrency: %s", err)
		}
	}

	// Create/update lambda function URL

	fOut, err := lambdaCl.GetFunctionUrlConfig(ctx, &lambda.GetFunctionUrlConfigInput{
		FunctionName: aws.String(fnName),
	})
	var fnUrl string
	if err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return nil, fmt.Errorf("failed to get lambda function url config: %s", err)
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
			return nil, fmt.Errorf("failed poll function state: %s", err)
		}
		switch s := fOut.Configuration.State; s {
		case lambdatypes.StateActive:
			break activeWait
		case lambdatypes.StatePending:
			time.Sleep(2 * time.Second)
			continue
		default:
			return nil, fmt.Errorf("invalid state while polling: %s", s)
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
*/
