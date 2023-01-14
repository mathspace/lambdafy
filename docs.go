package main

import (
	"embed"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"text/template"

	"github.com/spf13/cobra"
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

var exampleSpecCmd = &cobra.Command{
	Use:   "example-spec",
	Short: "Prints an example spec with extensive comments to stdout",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Print(exampleSpec)
		return nil
	},
}

var exampleRoleCmd = &cobra.Command{
	Use:   "example-role",
	Short: "Prints an example IAM role in terraform format to stdout",
	RunE: func(cmd *cobra.Command, args []string) error {
		inlinePol, err := serializeRolePolicy(nil)
		if err != nil {
			return err
		}
		tpl := template.Must(template.New("example-role").Parse(exampleRole))
		if err := tpl.Execute(os.Stdout, map[string]string{
			"AssumeRolePolicy": defaultAssumeRolePolicy,
			"InlinePolicy":     inlinePol,
		}); err != nil {
			return err
		}
		return nil
	},
}

var createSampleProjectCmd = &cobra.Command{
	Use:   "create-sample-project output-dir",
	Short: "Creates a sample project in the given directory",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {

		outDir := args[0]

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
