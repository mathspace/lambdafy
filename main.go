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

// beforeAppCmd runs tasks required before the app related commands are run.
func beforeAppCmd(c *cli.Context) error {
	var err error
	if c.Path("spec") == "-" {
		spec, err = appspec.Load(os.Stdin)
	} else {
		spec, err = appspec.LoadFromFile(c.Path("spec"))
	}
	if err != nil {
		return err
	}
	return nil
}

func main() {
	app := &cli.App{
		Name:        "lambdafy",
		Usage:       "Greatly simplifies deployment of stateless web apps on AWS",
		Description: "If .env file exists, it will be used to populate env vars.",
		Flags: []cli.Flag{
			&cli.PathFlag{
				Name:      "spec",
				Value:     cli.Path("lambdafy-spec.yaml"),
				Usage:     "Path to spec file used for all commands (in yaml format). Use - for stdin.",
				EnvVars:   []string{"LAMBDAFY_SPEC"},
				TakesFile: true,
			},
			&cli.StringFlag{
				Name:    "ecr-repo",
				Value:   "lambdafy",
				Usage:   "ECR repo `name` where lambdafied images will be pushed to",
				EnvVars: []string{"LAMBDAFY_ECR_REPO"},
			},
			&cli.StringFlag{
				Name:    "default-role",
				Value:   "lambdafy",
				Usage:   "Default role `ARN` to use for lambdas",
				EnvVars: []string{"LAMBDAFY_DEFAULT_ROLE"},
			},
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Value:   "text",
				Usage:   "Output `type`: must be one of text or json",
				EnvVars: []string{"LAMBDAFY_OUTPUT"},
			},
		},
		Commands: []*cli.Command{
			{
				Name:      "deploy",
				Usage:     "deploy the given image",
				ArgsUsage: "image-name",
				Before:    beforeAppCmd,
				Action: func(c *cli.Context) error {
					res, err := deployApp(c)
					if err != nil {
						return err
					}
					outp(res)
					return nil
				},
			},
			{
				Name:   "delete",
				Usage:  "delete the deployment",
				Before: beforeAppCmd,
				Action: deleteApp,
			},
			{
				Name:  "example-spec",
				Usage: "example example spec to stdout",
				Action: func(c *cli.Context) error {
					fmt.Print(exampleSpec)
					return nil
				},
			},
			{
				Name:  "example-custom-role",
				Usage: "example custom role in terraform format to stdout",
				Action: func(c *cli.Context) error {
					fmt.Print(exampleCustomRole)
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
