package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/urfave/cli/v2"
)

var infoCmd = &cli.Command{
	Name:      "info",
	Usage:     "print out info about a function",
	ArgsUsage: "function-name",
	Flags: []cli.Flag{
		versionFlag,
		&cli.StringFlag{
			Name:    "key",
			Aliases: []string{"k"},
			Usage:   "key to print the value of",
		},
	},
	Action: func(c *cli.Context) error {
		fnName := c.Args().First()
		fnVer := c.String("version")
		if c.NArg() != 1 || fnName == "" {
			return errors.New("must provide a function name as the only arg")
		}
		if fnVer == "" {
			return errors.New("must provide a version")
		}
		inf, err := info(fnName, fnVer)
		if err != nil {
			return err
		}
		k := c.String("key")
		if k != "" {
			v, ok := inf[k]
			if !ok {
				return fmt.Errorf("key '%s' not found", k)
			}
			fmt.Println(v)
			return nil
		}
		sortedKeys := make([]string, 0, len(inf))
		for k := range inf {
			sortedKeys = append(sortedKeys, k)
		}
		sort.Strings(sortedKeys)
		for _, k := range sortedKeys {
			fmt.Printf("%s=%s\n", k, inf[k])
		}
		return nil
	},
}

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
		fnVer = strconv.Itoa(vers[len(vers)-1].version)

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
	inf["resolved-image"] = *gfo.Code.ResolvedImageUri
	inf["role"] = *gfo.Configuration.Role
	inf["timestamp"] = *gfo.Configuration.LastModified
	return inf, nil
}
