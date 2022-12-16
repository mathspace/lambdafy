package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/urfave/cli/v2"
)

var infoCmd = &cli.Command{
	Name:      "info",
	Aliases:   []string{"i"},
	Usage:     "print out info about a function",
	ArgsUsage: "function-name",
	Action: func(c *cli.Context) error {
		if c.NArg() != 1 {
			return errors.New("must provide a function name as the only arg")
		}
		inf, err := info(c.Args().First())
		if err != nil {
			return err
		}
		fmt.Printf("name:%s\n", inf.name)
		fmt.Printf("image:%s\n", inf.image)
		fmt.Printf("resolved-image:%s\n", inf.resolvedImage)
		fmt.Printf("role:%s\n", inf.role)
		fmt.Printf("active-version:%s\n", inf.activeVersion)
		fmt.Printf("url:%s\n", inf.url)
		fmt.Printf("last-modified:%s\n", inf.lastUpdated)
		return nil
	},
}

type fnInfo struct {
	name          string
	activeVersion string
	url           string
	image         string
	resolvedImage string
	lastUpdated   string
	role          string
}

func info(fnName string) (fnInfo, error) {
	inf := fnInfo{name: fnName}
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return inf, fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	alias, err := lambdaCl.GetAlias(ctx, &lambda.GetAliasInput{
		FunctionName: &fnName,
		Name:         aws.String(activeAlias),
	})
	if err != nil && !strings.Contains(err.Error(), "404") {
		return inf, fmt.Errorf("failed to get active function alias: %s", err)
	}
	var activeVersion *string
	if err == nil {
		activeVersion = alias.FunctionVersion
		inf.activeVersion = *activeVersion
		fu, err := lambdaCl.GetFunctionUrlConfig(ctx, &lambda.GetFunctionUrlConfigInput{
			FunctionName: &fnName,
			Qualifier:    aws.String(activeAlias),
		})
		if err != nil {
			return inf, fmt.Errorf("failed to get function url: %s", err)
		}
		inf.url = *fu.FunctionUrl
	}

	gfo, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: &fnName,
		Qualifier:    activeVersion,
	})
	if err != nil {
		return inf, err
	}

	if gfo.Code.ImageUri == nil {
		return inf, fmt.Errorf("function %s is not an docker image function", fnName)
	}

	inf.role = *gfo.Configuration.Role
	inf.image = *gfo.Code.ImageUri
	inf.resolvedImage = *gfo.Code.ResolvedImageUri
	inf.lastUpdated = *gfo.Configuration.LastModified
	return inf, nil
}
