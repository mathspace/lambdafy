package main

import (
	"context"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

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
