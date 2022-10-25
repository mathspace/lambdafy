package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	_ "github.com/oxplot/starenv/autoload"
	"github.com/urfave/cli/v2"
)

type outputter func(map[string]string)

var (

	//go:embed example-spec.yaml
	exampleSpec string

	//go:embed example-role.tf
	exampleRole string

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
				Usage:     "modify the image by adding lambda proxy to it",
				ArgsUsage: "image-name",
				Action: func(c *cli.Context) error {
					if c.NArg() != 1 {
						return fmt.Errorf("must provide a docker image name as the only arg")
					}
					return lambdafyImage(c.Args().First())
				},
			},
			{
				Name:        "publish",
				Usage:       "publish a new version of a function without routing traffic to it",
				ArgsUsage:   "spec-file",
				Description: "Use '-' as spec-file to read from stdin.",
				Action: func(c *cli.Context) error {
					if c.NArg() != 1 {
						return errors.New("must provide a spec file as the only arg")
					}
					p := c.Args().First()
					var r io.Reader
					if p == "-" {
						r = os.Stdin
					} else {
						f, err := os.Open(p)
						if err != nil {
							return fmt.Errorf("failed to open spec file: %s", err)
						}
						defer f.Close()
						r = f
					}
					out, err := publish(r)
					if err != nil {
						return err
					}
					log.Printf("published new version: %s", out.version)
					return nil
				},
			},
			{
				Name:      "deploy",
				Usage:     "deploy a specific version of a function",
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
			{
				Name:  "example-role",
				Usage: "example IAM role in terraform format to stdout",
				Action: func(c *cli.Context) error {
					fmt.Print(exampleRole)
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
