package main

import (
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/oxplot/starenv/autoload"
	"github.com/urfave/cli/v2"
)

// Populated at build time by -X ldflag
var (
	Version      = "dev"
	Commit       = "HEAD"
	CompiledDate = "1970-01-01T00:00:00Z"
)

func main() {
	compiledDate, err := time.Parse(time.RFC3339, CompiledDate)
	if err != nil {
		panic(err)
	}
	app := &cli.App{
		Name:        "lambdafy",
		Usage:       "Use any docker image as a lambda function",
		Description: "If .env file exists, it will be used to populate env vars.",
		Version:     Version + " (" + Commit + ")",
		Copyright:   fmt.Sprintf("%d Mathspace Pty. Ltd.", time.Now().Year()),
		Compiled:    compiledDate,
		Commands: []*cli.Command{
			createSampleProjectCmd,
			makeCmd,
			publishCmd,
			deployCmd,
			undeployCmd,
			listCmd,
			infoCmd,
			logsCmd,
			versionsCmd,
			deleteCmd,
			exampleSpecCmd,
		},
		EnableBashCompletion: true,
		Suggest:              true,
	}
	log.SetFlags(0)
	if err := app.Run(os.Args); err != nil {
		log.Fatalf("error: %s", err)
	}
}
