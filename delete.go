package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/urfave/cli/v2"
)

var deleteCmd = &cli.Command{
	Name:      "delete",
	Usage:     "delete the function",
	ArgsUsage: "function-name",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "yes",
			Usage: "Actually delete the function",
		},
	},
	Action: func(c *cli.Context) error {
		if c.NArg() != 1 {
			return errors.New("must provide a function name as the only arg")
		}
		if !c.Bool("yes") {
			return errors.New("must pass --yes to actually delete the function")
		}
		return deleteFunction(c.Args().First())
	},
}

func deleteFunction(name string) error {
	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	if err := retryOnResourceConflict(func() error {
		_, err := lambdaCl.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
			FunctionName: &name,
		})
		return err
	}); err != nil && !strings.Contains(err.Error(), "404") {
		return err
	}

	return nil
}
