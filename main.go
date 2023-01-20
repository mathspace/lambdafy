package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"text/template"

	_ "github.com/oxplot/starenv/autoload"
	"github.com/spf13/cobra"
)

// These will be populated by goreleaser.
var (
	version = "dev"
	commit  = "HEAD"
)

// Global flags.
var (
	outputTemplate string
)

// formatOutput formats the output of a command.
func formatOutput(v interface{}) error {

	// Encode to JSON because we will either decode it to a simple map for
	// templating or print it as is if no template is provided. The reason for this
	// is that lowercase field names are not supported in templates but lowercase
	// map keys are. We also do not want to have a mix of casing (spec is snake
	// case).

	b := bytes.Buffer{}
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("failed to encode output: %s", err)
	}

	if outputTemplate == "" {
		fmt.Print(b.String())
		return nil
	}

	var w interface{}
	if err := json.Unmarshal(b.Bytes(), &w); err != nil {
		return fmt.Errorf("failed to decode output: %s", err)
	}
	tpl, err := template.New("output").Option("missingkey=error").Parse(outputTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse output template: %s", err)
	}
	return tpl.Execute(os.Stdout, w)

}

func main() {
	app := &cobra.Command{
		Use:     "lambdafy",
		Short:   "Use any docker image as a lambda function",
		Version: fmt.Sprintf("%s (%s)", version, commit),
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			cmd.SilenceUsage = true
		},
	}
	app.PersistentFlags().StringVarP(&outputTemplate, "output", "o", "", "Output go style template")

	app.AddCommand(aliasCmd)
	app.AddCommand(cleanupRolesCmd)
	app.AddCommand(createSampleProjectCmd)
	app.AddCommand(deleteCmd)
	app.AddCommand(deployCmd)
	app.AddCommand(exampleRoleCmd)
	app.AddCommand(exampleSpecCmd)
	app.AddCommand(infoCmd)
	app.AddCommand(listCmd)
	app.AddCommand(logsCmd)
	app.AddCommand(makeCmd)
	app.AddCommand(publishCmd)
	app.AddCommand(pushCmd)
	app.AddCommand(specCmd)
	app.AddCommand(sqsCmd)
	app.AddCommand(unaliasCmd)
	app.AddCommand(undeployCmd)
	app.AddCommand(versionsCmd)

	log.SetFlags(0)
	if err := app.Execute(); err != nil {
		os.Exit(1)
	}
}
