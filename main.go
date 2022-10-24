package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"

	_ "github.com/oxplot/starenv/autoload"
	"github.com/urfave/cli/v2"

	"github.com/mathspace/lambdafy/appspec"
)

type outputter func(map[string]string)

var (
	spec *appspec.AppSpec

	//go:embed example-app-spec.yaml
	exampleSpec string

	//go:embed example-custom-role.tf
	exampleCustomRole string

	outputters = map[string]outputter{
		"json": func(kv map[string]string) {
			e := json.NewEncoder(os.Stdout)
			e.SetIndent("", "  ")
			e.Encode(kv)
		},
		"text": func(kv map[string]string) {
			for k, v := range kv {
				fmt.Printf("%s: %s\n", k, v)
			}
		},
	}

	outp outputter
)

func main() {
	app := &cli.App{
		Name:        "lambdafy",
		Usage:       "Greatly simplifies deployment of lambda functions on AWS",
		Description: "If .env file exists, it will be used to populate env vars.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Value:   "text",
				Usage:   "Output `type`: must be one of text or json",
			},
		},
		Commands: []*cli.Command{
			{
				Name:      "make",
				Usage:     "modify the image by adding lambda proxy to it (no-op if already done)",
				ArgsUsage: "image-name",
			},
			{
				Name:      "publish",
				Usage:     "publish a new version of a function (or create function if new)",
				ArgsUsage: "spec-file",
			},
			{
				Name:      "deploy",
				Usage:     "deploy the image",
				ArgsUsage: "function-name version",
			},
			{
				Name:      "delete",
				Usage:     "delete the function",
				ArgsUsage: "function-name",
			},
			{
				Name:    "list",
				Aliases: []string{"ls"},
				Usage:   "list functions",
			},
			{
				Name:      "info",
				Aliases:   []string{"i"},
				Usage:     "print out info about a function",
				ArgsUsage: "function-name",
			},
			{
				Name:      "versions",
				Aliases:   []string{"ver"},
				Usage:     "list versions of a function",
				ArgsUsage: "function-name",
			},
			{
				Name:  "example-spec",
				Usage: "example spec to stdout",
				Action: func(c *cli.Context) error {
					fmt.Print(exampleSpec)
					return nil
				},
			},
		},
		Before: func(c *cli.Context) error {
			outp = outputters[c.String("output")]
			if outp == nil {
				return fmt.Errorf("--output must be text or json")
			}
			return nil
		},
		EnableBashCompletion: true,
		Suggest:              true,
	}
	log.SetFlags(0)
	if err := app.Run(os.Args); err != nil {
		log.Fatalf("error: %s", err)
	}
}
