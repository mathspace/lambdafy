package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/spf13/cobra"
)

var infoCmd *cobra.Command

func init() {
	var ver string
	infoCmd = &cobra.Command{
		Use:   "info function-name",
		Short: "Print out info about a function",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fnName := args[0]
			inf, err := info(fnName, ver)
			if err != nil {
				return err
			}
			return formatOutput(inf)
		},
	}
	addVersionFlag(infoCmd.Flags(), &ver)
}

// info returns information about a function.
func info(fnName string, fnVer string) (map[string]string, error) {
	inf := map[string]string{
		"name": fnName,
	}
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return inf, fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	// We kind of re-implement the login of versionFlag here, but it's necessary
	// because there are minor differences and we also need to get more details
	// out of the function alias.

	if fnVer == latestPseudoVersion {
		fnVer = "latest"
		vers, err := versions(fnName)
		if err != nil {
			return inf, fmt.Errorf("failed to get versions: %s", err)
		}
		fnVer = strconv.Itoa(vers[len(vers)-1].Version)

	} else if _, err := strconv.Atoi(fnVer); err != nil { // not a number
		alias, err := lambdaCl.GetAlias(ctx, &lambda.GetAliasInput{
			FunctionName: &fnName,
			Name:         &fnVer,
		})
		if err != nil && !strings.Contains(err.Error(), "404") {
			return inf, fmt.Errorf("failed to lookup function alias: %s", err)
		}

		if err == nil {
			inf["version"] = *alias.FunctionVersion
			fu, err := lambdaCl.GetFunctionUrlConfig(ctx, &lambda.GetFunctionUrlConfigInput{
				FunctionName: &fnName,
				Qualifier:    &fnVer,
			})
			if err != nil {
				return inf, fmt.Errorf("failed to get function url: %s", err)
			}
			inf["url"] = *fu.FunctionUrl
		}
	}

	gfo, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: &fnName,
		Qualifier:    &fnVer,
	})
	if err != nil {
		return inf, err
	}

	if gfo.Code.ImageUri == nil {
		return inf, fmt.Errorf("function %s is not an docker image function", fnName)
	}

	inf["version"] = *gfo.Configuration.Version
	inf["image"] = *gfo.Code.ImageUri
	inf["resolved_image"] = *gfo.Code.ResolvedImageUri
	inf["role"] = *gfo.Configuration.Role
	inf["timestamp"] = *gfo.Configuration.LastModified
	return inf, nil
}
