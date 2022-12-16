package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/urfave/cli/v2"
)

const latestPseudoVersion = "latest"

// resolveVersion resolves the given version spec to an actual version. If a
// numerical version is provided, it will be returned as is. Otherwise, function
// aliase names are looked up. "latest" is a special case referring to the
// latest version of the function.
func resolveVersion(fnName string, verSpec string) (int, error) {
	if verSpec == "" {
		return 0, errors.New("version spec must not be empty")
	}
	if v, err := strconv.Atoi(verSpec); err == nil {
		return v, nil
	}
	if verSpec == latestPseudoVersion {
		vers, err := versions(fnName)
		if err != nil {
			return 0, fmt.Errorf("failed lookup latest version: %s", err)
		}
		return vers[len(vers)-1].version, nil
	}

	lookupVer := &verSpec

	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	alias, err := lambdaCl.GetAlias(ctx, &lambda.GetAliasInput{
		FunctionName: &fnName,
		Name:         lookupVer,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get alias: %s", err)
	}

	vint, err := strconv.Atoi(*alias.FunctionVersion)
	if err != nil {
		return 0, fmt.Errorf("failed to parse version: %s", err)
	}

	return vint, nil
}

var versionFlag = &cli.StringFlag{
	Name:    "version",
	Aliases: []string{"v"},
	Usage:   "the version/alias of the function (use 'latest' for latest version)",
	Value:   activeAlias,
}

var versionsCmd = &cli.Command{
	Name:      "versions",
	Aliases:   []string{"ver", "version"},
	Usage:     "list versions of a function",
	ArgsUsage: "function-name",
	Action: func(c *cli.Context) error {
		fnName := c.Args().First()
		if c.NArg() != 1 || fnName == "" {
			return errors.New("must provide a function name as the only arg")
		}
		vers, err := versions(fnName)
		if err != nil {
			return err
		}
		for _, v := range vers {
			fmt.Printf("%d:%s:%s\n", v.version, strings.Join(v.aliases, ","), v.description)
		}
		return nil
	},
}

type fnVersion struct {
	version     int
	aliases     []string
	description string
}

func versions(fnName string) ([]fnVersion, error) {

	vs := []fnVersion{}

	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	// Get all aliases and map them from function version to alias name.

	aliases := map[string][]string{}
	ap := lambda.NewListAliasesPaginator(lambdaCl, &lambda.ListAliasesInput{
		FunctionName: &fnName,
	})
	for ap.HasMorePages() {
		page, err := ap.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list aliases: %s", err)
		}
		for _, a := range page.Aliases {
			fa, fv := *a.Name, *a.FunctionVersion
			aliases[fv] = append(aliases[fv], fa)
		}
	}
	for _, a := range aliases {
		sort.StringSlice(a).Sort()
	}

	p := lambda.NewListVersionsByFunctionPaginator(lambdaCl, &lambda.ListVersionsByFunctionInput{
		FunctionName: &fnName,
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list versions: %s", err)
		}
		for _, v := range page.Versions {
			if *v.Version != "$LATEST" {
				intVer, err := strconv.Atoi(*v.Version)
				if err != nil {
					return nil, fmt.Errorf("failed to convert version to int: %s", err)
				}
				vs = append(vs, fnVersion{
					version:     intVer,
					aliases:     aliases[*v.Version],
					description: *v.Description,
				})
			}
		}
	}

	sort.Slice(vs, func(i, j int) bool {
		return vs[i].version < vs[j].version
	})

	return vs, nil
}
