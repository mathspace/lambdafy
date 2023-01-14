package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
		return vers[len(vers)-1].Version, nil
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

func addVersionFlag(c *pflag.FlagSet, ver *string) {
	c.StringVarP(ver, "version", "v", activeAlias, "the version/alias of the function (use 'latest' for latest version)")
}

var versionsCmd = &cobra.Command{
	Use:     "versions",
	Aliases: []string{"ver", "version"},
	Short:   "List versions of a function",
	Args:    cobra.ExactArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		fnName := args[0]
		vers, err := versions(fnName)
		if err != nil {
			return err
		}
		return formatOutput(vers)
	},
}

type fnVersion struct {
	Version     int      `json:"version"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
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
				al := aliases[*v.Version]
				if al == nil {
					al = []string{}
				}
				vs = append(vs, fnVersion{
					Version:     intVer,
					Aliases:     al,
					Description: *v.Description,
				})
			}
		}
	}

	sort.Slice(vs, func(i, j int) bool {
		return vs[i].Version < vs[j].Version
	})

	return vs, nil
}
