package main

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
	dockerjsonmsg "github.com/docker/docker/pkg/jsonmessage"
	"github.com/urfave/cli/v2"
)

//go:generate sh -c "cd proxy ; CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../proxy-linux-amd64"

var (
	//go:embed proxy-linux-amd64
	proxyBinary []byte
)

// processDockerResponse decodes the JSON line stream of docker daemon
// and determines if there is any error. All other output is discarded.
func processDockerResponse(r io.Reader) error {
	d := json.NewDecoder(r)
	for {
		var m dockerjsonmsg.JSONMessage
		if err := d.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if m.Error != nil {
			return errors.New(m.Error.Message)
		}
	}
}

func isAccountRegionAllowed(ctx context.Context, acfg aws.Config) (bool, error) {
	stsCl := sts.NewFromConfig(acfg)
	cid, err := stsCl.GetCallerIdentity(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("failed to get aws account number: %s", err)
	}
	return spec.IsAccountRegionAllowed(*cid.Account, acfg.Region), nil
}

type deployResult struct {
	url string
}

// deployApp creates/updates necessary resources for running a lambda function
// with the given image and spec, exposed via a public URL.
func deployApp(c *cli.Context) (*deployResult, error) {
	if c.NArg() != 1 {
		return nil, fmt.Errorf("must provide a docker image name as first arg")
	}
	ctx := context.Background()

	log.Print("setting up AWS and docker clients")

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

	log.Print("building lambda compatible docker image")

	inImage := c.Args().First()
	t := time.UnixMicro(0)
	outImage := fmt.Sprintf("lambdafied:%d", t.UnixNano())

	img, _, err := dc.ImageInspectWithRaw(ctx, inImage)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect docker image: %s", err)
	}

	if img.Architecture != "amd64" || img.Os != "linux" {
		return nil, errors.New("image platform must be linux/amd64")
	}

	ep, err := json.Marshal(append([]string{"/lambdafy-proxy"}, img.Config.Entrypoint...))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal entrypoint to json: %s", err)
	}

	dockerFile := fmt.Sprintf(`
FROM --platform=linux/amd64 %s
COPY --chmod=775 lambdafy-proxy /
ENTRYPOINT %s
`, inImage, string(ep))

	r, w := io.Pipe()

	go func() {
		tr := tar.NewWriter(w)
		_ = tr.WriteHeader(&tar.Header{Name: "Dockerfile", Size: int64(len(dockerFile)), ModTime: t, AccessTime: t, ChangeTime: t})
		_, _ = tr.Write([]byte(dockerFile))
		_ = tr.WriteHeader(&tar.Header{Name: "lambdafy-proxy", Size: int64(len(proxyBinary)), ModTime: t, AccessTime: t, ChangeTime: t})
		_, _ = tr.Write(proxyBinary)
		_ = tr.Close()
		_ = w.Close()
	}()

	resp, err := dc.ImageBuild(ctx, r, dockertypes.ImageBuildOptions{
		Tags:           []string{outImage},
		Version:        dockertypes.BuilderBuildKit,
		Platform:       "linux/amd64",
		SuppressOutput: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build image: %s", err)
	}
	if err := processDockerResponse(resp.Body); err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("failed to build image: %s", err)
	}
	resp.Body.Close()

	log.Print("determining ECR full repo URL")

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

	log.Print("tagging image")

	img, _, err = dc.ImageInspectWithRaw(ctx, outImage)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect image: %s", err)
	}
	idSum := sha256.Sum256([]byte(img.ID))
	repoImage := fmt.Sprintf("%s:%s", *repoUri, hex.EncodeToString(idSum[:]))

	if err := dc.ImageTag(ctx, outImage, repoImage); err != nil {
		return nil, fmt.Errorf("failed to tag image: %s", err)
	}

	defer dc.ImageRemove(ctx, outImage, dockertypes.ImageRemoveOptions{
		Force: true,
	})

	log.Print("logging in to ECR")

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

	log.Printf("pushing %s", repoImage)

	authCfg := dockertypes.AuthConfig{
		Username:      authTokenParts[0],
		Password:      authTokenParts[1],
		ServerAddress: regEP,
	}
	authCfgBytes, _ := json.Marshal(authCfg)
	authCfgEncoded := base64.URLEncoding.EncodeToString(authCfgBytes)

	rc, err := dc.ImagePush(ctx, repoImage, dockertypes.ImagePushOptions{
		RegistryAuth: authCfgEncoded,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to push image: %s", err)
	}
	if err := processDockerResponse(rc); err != nil {
		rc.Close()
		return nil, fmt.Errorf("failed to push image: %s", err)
	}
	rc.Close()

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

		log.Printf("creating new lambda function %s", fnName)

		_, err := lambdaCl.CreateFunction(ctx, &lambda.CreateFunctionInput{
			FunctionName:  aws.String(fnName),
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
		log.Printf("updating existing lambda function %s", fnName)

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

		for {
			if _, err := lambdaCl.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
				FunctionName: aws.String(fnName),
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

	if _, err := lambdaCl.PutFunctionConcurrency(ctx, &lambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String(fnName),
		ReservedConcurrentExecutions: aws.Int32(spec.ReservedConcurrency),
	}); err != nil {
		return nil, fmt.Errorf("failed to set reserved concurrency: %s", err)
	}

	// Create/update lambda function URL

	fOut, err := lambdaCl.GetFunctionUrlConfig(ctx, &lambda.GetFunctionUrlConfigInput{
		FunctionName: aws.String(fnName),
	})
	if err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return nil, fmt.Errorf("failed to get lambda function url config: %s", err)
		}
		log.Print("creating lambda function url")

		fOut, err := lambdaCl.CreateFunctionUrlConfig(ctx, &lambda.CreateFunctionUrlConfigInput{
			AuthType:     lambdatypes.FunctionUrlAuthTypeNone,
			FunctionName: aws.String(fnName),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create function url: %s", err)
		}
		return &deployResult{url: *fOut.FunctionUrl}, nil
	} else {
		return &deployResult{url: *fOut.FunctionUrl}, nil
	}

}

func deleteApp(c *cli.Context) error {
	// TODO
	return nil
}
