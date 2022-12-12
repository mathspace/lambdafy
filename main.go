package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	_ "github.com/oxplot/starenv/autoload"
	"github.com/urfave/cli/v2"
)

type outputter func(map[string]string)

var (
	//go:embed example-spec.yaml
	exampleSpec string

	//go:embed example-role.tf
	exampleRole string
)

func main() {
	app := &cli.App{
		Name:        "lambdafy",
		Usage:       "Use any docker image as a lambda function",
		Description: "If .env file exists, it will be used to populate env vars.",
		Commands: []*cli.Command{
			{
				Name:      "make",
				Usage:     "modify the image by adding lambdafy proxy to it",
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
					fmt.Printf("function-name:%s\n", out.name)
					fmt.Printf("published-version:%s\n", out.version)
					return nil
				},
			},
			{
				Name:      "deploy",
				Usage:     "deploy a specific version of a function to a public URL",
				ArgsUsage: "function-name version",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:  "prime",
						Usage: "prime the function by sending it concurrent requests",
						Value: 1,
					},
				},
				Action: func(c *cli.Context) error {
					prime := c.Int("prime")
					if prime < 1 || prime > 100 {
						return fmt.Errorf("--prime must be between 1 and 100")
					}
					if c.NArg() != 2 {
						return errors.New("must provide a function name and version as args")
					}
					fnName := c.Args().Get(0)
					version := c.Args().Get(1)

					fnURL, err := deploy(fnName, version, prime)
					if err != nil {
						return err
					}
					log.Printf("deployed function '%s' version '%s' to '%s'", fnName, version, fnURL)
					return nil
				},
			},
			{
				Name:      "undeploy",
				Usage:     "remove deployment and make function inaccessible",
				ArgsUsage: "function-name",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "yes",
						Usage: "Actually undeploy the function",
					},
				},
				Action: func(c *cli.Context) error {
					if c.NArg() != 1 {
						return errors.New("must provide a function name as the only arg")
					}
					if !c.Bool("yes") {
						return errors.New("must pass --yes to actually undeploy the function")
					}
					if err := undeploy(c.Args().First()); err != nil {
						return err
					}
					return nil
				},
			},
			{
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
			},
			{
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
			},
			{
				Name:      "info",
				Aliases:   []string{"i"},
				Usage:     "print out info about a function",
				ArgsUsage: "function-name",
				Action: func(c *cli.Context) error {
					if c.NArg() != 1 {
						return errors.New("must provide a function name as the only arg")
					}
					inf, err := info(c.Args().First())
					if err != nil {
						return err
					}
					fmt.Printf("name:%s\n", inf.name)
					fmt.Printf("image:%s\n", inf.image)
					fmt.Printf("resolved-image:%s\n", inf.resolvedImage)
					fmt.Printf("role:%s\n", inf.role)
					fmt.Printf("active-version:%s\n", inf.activeVersion)
					fmt.Printf("url:%s\n", inf.url)
					fmt.Printf("last-modified:%s\n", inf.lastUpdated)
					return nil
				},
			},
			{
				Name:      "logs",
				Aliases:   []string{"log"},
				Usage:     "print out most recent logs for the function",
				ArgsUsage: "function-name",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:        "version",
						Aliases:     []string{"v"},
						Usage:       "the version of the function to get logs for",
						DefaultText: "latest",
					},
					&cli.BoolFlag{
						Name:    "tail",
						Aliases: []string{"t"},
						Usage:   "wait for new logs and print them as they come in",
					},
					&cli.UintFlag{
						Name:    "since",
						Aliases: []string{"s"},
						Usage:   "only print logs since this many minutes ago",
						Value:   1,
					},
				},
				Action: func(c *cli.Context) error {
					if c.NArg() != 1 {
						return errors.New("must provide a function name as the only arg")
					}
					since := time.Now().Add(-time.Duration(c.Uint("since")) * time.Minute)
					fnName := c.Args().First()
					version := c.String("version")
					// If version is empty, get the latest version
					if version == "" {
						inf, err := info(fnName)
						if err != nil {
							return fmt.Errorf("failed to get function info: %s", err)
						}
						version = inf.activeVersion
						if version == "" {
							return fmt.Errorf("no active version found - manually specify version with --version")
						}
						log.Printf("printing logs for version %s", version)
					}

					var afterToken string
					for {
						lgs, err := logs(fnName, version, since, afterToken)
						if err != nil {
							return err
						}
						for _, l := range lgs.lines {
							fmt.Println(l)
						}
						if !c.Bool("tail") {
							return nil
						}
						afterToken = lgs.afterToken
						since = time.Now().Add(-30 * time.Second)
						time.Sleep(2 * time.Second)
					}
				},
			},
			{
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
			{
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
			},
		},
		EnableBashCompletion: true,
		Suggest:              true,
	}
	log.SetFlags(0)
	if err := app.Run(os.Args); err != nil {
		log.Fatalf("error: %s", err)
	}
}
