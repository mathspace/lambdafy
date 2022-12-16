package main

import (
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/oxplot/starenv/autoload"
	"github.com/urfave/cli/v2"
)

// These will be populated by goreleaser.
var (
	version = "dev"
	commit  = "HEAD"
	date    = "1970-01-01T00:00:00Z"
)

func main() {
	compiledDate, err := time.Parse(time.RFC3339, date)
	if err != nil {
		panic(err)
	}
	app := &cli.App{
		Name:        "lambdafy",
		Usage:       "Use any docker image as a lambda function",
		Description: "If .env file exists, it will be used to populate env vars. Starenv autoloader is also used. See https://pkg.go.dev/github.com/oxplot/starenv for more details.",
		Version:     version + " (" + commit + ")",
		Copyright:   fmt.Sprintf("%d Mathspace Pty. Ltd.", compiledDate.Year()),
		Compiled:    compiledDate,
		Commands: []*cli.Command{
			createSampleProjectCmd,
			makeCmd,
			publishCmd,
			deployCmd,
			undeployCmd,
			listCmd,
			infoCmd,
			specCmd,
			logsCmd,
			versionsCmd,
			deleteCmd,
			exampleSpecCmd,
			exampleRoleCmd,
		},
		EnableBashCompletion: true,
		Suggest:              true,
	}
	log.SetFlags(0)
	if err := app.Run(os.Args); err != nil {
		log.Fatalf("error: %s", err)
	}
}
