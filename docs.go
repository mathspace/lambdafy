package main

import (
	"embed"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v2"
)

const sampleProjectDir = "sample-project"

var (
	//go:embed example-spec.yaml
	exampleSpec string

	//go:embed example-role.tf
	exampleRole string

	//go:embed sample-project
	sampleProject embed.FS
)

var exampleSpecCmd = &cli.Command{
	Name:  "example-spec",
	Usage: "prints an example spec with extensive comments to stdout",
	Action: func(c *cli.Context) error {
		fmt.Print(exampleSpec)
		return nil
	},
}

var exampleRoleCmd = &cli.Command{
	Name:  "example-role",
	Usage: "prints an example IAM role in terraform format to stdout",
	Action: func(c *cli.Context) error {
		fmt.Print(exampleRole)
		return nil
	},
}

var createSampleProjectCmd = &cli.Command{
	Name:      "create-sample-project",
	Usage:     "creates a sample project in the given directory",
	ArgsUsage: "output-dir",
	Action: func(c *cli.Context) error {
		outDir := c.Args().Get(0)
		if c.NArg() != 1 || outDir == "" {
			return errors.New("must provide a directory as the only arg")
		}

		// Create the output directory if it doesn't exist

		if err := os.MkdirAll(outDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory %s: %w", outDir, err)
		}

		// Copy the files over

		d, _ := sampleProject.ReadDir(sampleProjectDir)
		for _, f := range d {
			p := filepath.Join(sampleProjectDir, f.Name())
			b, _ := sampleProject.ReadFile(p)
			outPath := filepath.Join(outDir, f.Name())
			if err := ioutil.WriteFile(outPath, b, 0644); err != nil {
				return fmt.Errorf("failed to write file %s: %w", outPath, err)
			}
		}

		// Make run.sh executable

		if err := os.Chmod(filepath.Join(outDir, "run.sh"), 0755); err != nil {
			return fmt.Errorf("failed to make run.sh executable: %w", err)
		}

		log.Printf("Created sample project in '%s'.", outDir)
		log.Printf("See '%s/run.sh' to get started.", outDir)
		return nil
	},
}
