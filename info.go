package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

type fnInfo struct {
	name          string
	activeVersion string
	image         string
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
	}

	gfo, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: &fnName,
		Qualifier:    activeVersion,
	})
	if err != nil {
		return inf, err
	}

	inf.role = *gfo.Configuration.Role
	inf.image = *gfo.Code.ImageUri
	inf.lastUpdated = *gfo.Configuration.LastModified
	return inf, nil
}
