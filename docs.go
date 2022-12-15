package main

import (
	_ "embed"
	"errors"

	"github.com/urfave/cli/v2"
)

var (
	//go:embed example-spec.yaml
	exampleSpec string

	//go:embed example-role.tf
	exampleRole string
)

var createSampleProjectCmd = &cli.Command{
	Name:      "create-sample-project",
	Usage:     "creates a sample project in the given directory",
	ArgsUsage: "directory",
	Action: func(c *cli.Context) error {
		if c.NArg() != 1 {
			return errors.New("must provide a directory as the only arg")
		}
		// TODO
		return nil
	},
}
