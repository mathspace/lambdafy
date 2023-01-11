package main

import (
	"fmt"
	"log"
	"os"

	_ "github.com/oxplot/starenv/autoload"
	"github.com/spf13/cobra"
)

// These will be populated by goreleaser.
var (
	version = "dev"
	commit  = "HEAD"
)

func main() {
	app := &cobra.Command{
		Use:     "lambdafy",
		Short:   "Use any docker image as a lambda function",
		Version: fmt.Sprintf("%s (%s)", version, commit),
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			cmd.SilenceUsage = true
		},
	}

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
	app.AddCommand(specCmd)
	app.AddCommand(unaliasCmd)
	app.AddCommand(undeployCmd)
	app.AddCommand(versionsCmd)

	log.SetFlags(0)
	if err := app.Execute(); err != nil {
		os.Exit(1)
	}
}
