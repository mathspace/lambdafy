package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	dockertypes "github.com/docker/docker/api/types"
	dockerclient "github.com/docker/docker/client"
	"github.com/spf13/cobra"
)

var pushCmd *cobra.Command

func init() {
	var create bool
	pushCmd = &cobra.Command{
		Use:   "push image-name[:tag] repo-name",
		Short: "Pushes a docker image to a ECR repository",
		Long:  "Pushes a docker image to a ECR repository. The pushed image URI is printed to stdout on success.",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			repoImage, err := push(args[0], args[1], create)
			if err != nil {
				return err
			}
			fmt.Println(repoImage)
			return nil
		},
	}
	pushCmd.Flags().BoolVarP(&create, "create", "c", false, "Create the repository if it doesn't exist")
}

// push pushes a docker image to a ECR repository.
// Returns the full ECR image URI.
func push(imgName string, repoName string, create bool) (string, error) {

	ctx := context.Background()

	if strings.Contains(repoName, ":") {
		return "", errors.New("repo-name cannot contain a tag - a unique tag is generated automatically")
	}

	// Setup clients

	dc, err := dockerclient.NewClientWithOpts(
		dockerclient.WithAPIVersionNegotiation(),
		dockerclient.FromEnv,
	)
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %s", err)
	}

	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load aws config: %s", err)
	}
	ecrCl := ecr.NewFromConfig(acfg)

	// Get the image digest rehash it to MD5 for more compact representation.

	img, _, err := dc.ImageInspectWithRaw(ctx, imgName)
	if err != nil {
		return "", fmt.Errorf("failed to inspect image '%s': %s", imgName, err)
	}
	imgDigestParts := strings.Split(img.ID, ":")
	if len(imgDigestParts) != 2 {
		return "", fmt.Errorf("invalid image digest '%s'", img.ID)
	}
	imgDigest := imgDigestParts[1]

	// Ensure the image is built for the correct platform.

	if img.Architecture != "amd64" || img.Os != "linux" {
		return "", fmt.Errorf("platform of docker image '%s' must be linux/amd64", imgName)
	}

	log.Print("logging in to ECR")

	tokResp, err := ecrCl.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return "", fmt.Errorf("failed to get ecr auth token: %s", err)
	}
	if len(tokResp.AuthorizationData) < 1 {
		return "", fmt.Errorf("missing ecr auth token")
	}
	authToken, err := base64.StdEncoding.DecodeString(*tokResp.AuthorizationData[0].AuthorizationToken)
	if err != nil {
		return "", fmt.Errorf("failed to decode ecr auth token: %s", err)
	}
	authTokenParts := strings.SplitN(string(authToken), ":", 2)
	if len(authTokenParts) != 2 {
		return "", errors.New("invalid ecr auth token")
	}
	regEP := *tokResp.AuthorizationData[0].ProxyEndpoint

	authCfg := dockertypes.AuthConfig{
		Username:      authTokenParts[0],
		Password:      authTokenParts[1],
		ServerAddress: regEP,
	}
	authCfgBytes, _ := json.Marshal(authCfg)
	authCfgEncoded := base64.URLEncoding.EncodeToString(authCfgBytes)

	// Get the ECR URI for the repo name

	var repoURL string
	o, err := ecrCl.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
		RepositoryNames: []string{repoName},
	})
	if err != nil {
		if strings.Contains(err.Error(), "RepositoryNotFoundException") {
			if !create {
				return "", fmt.Errorf("repository '%s' not found", repoName)
			}
			log.Printf("creating repository '%s' in ECR", repoName)
			_, err = ecrCl.CreateRepository(ctx, &ecr.CreateRepositoryInput{
				RepositoryName: &repoName,
			})
			if err != nil {
				return "", fmt.Errorf("failed to create repository '%s': %s", repoName, err)
			}
			o, err = ecrCl.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
				RepositoryNames: []string{repoName},
			})
			if err != nil {
				return "", fmt.Errorf("failed to describe repository '%s': %s", repoName, err)
			}
			repoURL = *o.Repositories[0].RepositoryUri
		} else {
			return "", fmt.Errorf("failed to describe repository '%s': %s", repoName, err)
		}
	}
	repoURL = *o.Repositories[0].RepositoryUri
	repoImage := repoURL + ":" + imgDigest

	log.Printf("tagging image")

	dc.ImageTag(ctx, imgName, repoImage)

	log.Print("pushing image to ECR")

	rc, err := dc.ImagePush(ctx, repoImage, dockertypes.ImagePushOptions{
		RegistryAuth: authCfgEncoded,
	})
	if err != nil {
		return "", fmt.Errorf("failed to push image '%s': %s", repoImage, err)
	}
	if err := processDockerResponse(rc); err != nil {
		rc.Close()
		return "", fmt.Errorf("failed to push tagged image '%s': %s", repoImage, err)
	}
	rc.Close()

	return repoImage, nil
}
