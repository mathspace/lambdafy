package main

import (
	"context"
	"fmt"
	"sort"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/urfave/cli/v2"
)

var listCmd = &cli.Command{
	Name:    "list",
	Aliases: []string{"ls"},
	Usage:   "list functions",
	Action: func(c *cli.Context) error {
		fns, err := listFunctions()
		if err != nil {
			return err
		}
		for _, f := range fns {
			fmt.Println(f)
		}
		return nil
	},
}

// listFunctions lists all lambdafy functions.
func listFunctions() ([]string, error) {
	fns := []string{}
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	listPages := lambda.NewListFunctionsPaginator(lambdaCl, &lambda.ListFunctionsInput{})
	for listPages.HasMorePages() {
		p, err := listPages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, f := range p.Functions {
			fns = append(fns, *f.FunctionName)
		}
	}
	sort.Strings(fns)
	return fns, nil
}
