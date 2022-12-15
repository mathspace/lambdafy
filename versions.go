package main

import (
	"context"
	"errors"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/urfave/cli/v2"
)

var versionsCmd = &cli.Command{
	Name:      "versions",
	Aliases:   []string{"ver"},
	Usage:     "list versions of a function",
	ArgsUsage: "function-name",
	Action: func(c *cli.Context) error {
		if c.NArg() != 1 {
			return errors.New("must provide a function name as the only arg")
		}
		fnName := c.Args().First()
		inf, err := info(fnName)
		if err != nil {
			return err
		}

		vs, err := versions(fnName)
		if err != nil {
			return err
		}
		for _, v := range vs {
			if inf.activeVersion == v.version {
				fmt.Printf("%s:%s (active)\n", v.version, v.description)
			} else {
				fmt.Printf("%s:%s\n", v.version, v.description)
			}
		}
		return nil
	},
}

type fnVersion struct {
	version     string
	description string
}

func versions(fnName string) ([]fnVersion, error) {

	vs := []fnVersion{}

	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	p := lambda.NewListVersionsByFunctionPaginator(lambdaCl, &lambda.ListVersionsByFunctionInput{
		FunctionName: &fnName,
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list versions: %s", err)
		}
		for _, v := range page.Versions {
			if *v.Version != "$LATEST" {
				vs = append(vs, fnVersion{version: *v.Version, description: *v.Description})
			}
		}
	}

	return vs, nil
}
