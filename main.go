package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
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

// resolveVersion resolves the given version spec to an actual version. If a
// numerical version is provided, it will be returned as is. If the version is
// "active", the acitve version will be returned. If the version is "latest",
// the latest version will be returned.
func resolveVersion(fnName string, verSpec string) (string, error) {
	switch verSpec {
	case "active":
		inf, err := info(fnName)
		if err != nil {
			return "", fmt.Errorf("failed to get function info: %s", err)
		}
		verSpec = inf.activeVersion
		if verSpec == "" {
			return "", fmt.Errorf("no active version found")
		}
	case "latest":
		vers, err := versions(fnName)
		if err != nil {
			return "", fmt.Errorf("failed to get function versions: %s", err)
		}
		if len(vers) == 0 {
			return "", fmt.Errorf("no versions found")
		}
		verSpec = vers[len(vers)-1].version
	default:
		if _, err := strconv.Atoi(verSpec); err != nil {
			return "", fmt.Errorf("invalid version spec: %s", verSpec)
		}
	}
	return verSpec, nil
}

var versionFlag = &cli.StringFlag{
	Name:    "version",
	Aliases: []string{"v"},
	Usage:   "the version of the function (active/latest or version number)",
	Value:   "active",
}

func main() {
	compiledDate, err := time.Parse(time.RFC3339, date)
	if err != nil {
		panic(err)
	}
	app := &cli.App{
		Name:        "lambdafy",
		Usage:       "Use any docker image as a lambda function",
		Description: "If .env file exists, it will be used to populate env vars.",
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
