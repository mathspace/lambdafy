package main

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/mathspace/lambdafy/fnspec"
	"github.com/spf13/cobra"
)

var specCmd *cobra.Command

func init() {
	var ver string
	specCmd = &cobra.Command{
		Use:   "spec function-name",
		Short: "Generate a function spec from published function",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fnName := args[0]
			version, err := resolveVersion(fnName, ver)
			if err != nil {
				return fmt.Errorf("failed to resolve version: %s", err)
			}

			s, err := generateSpec(fnName, version)
			if err != nil {
				return fmt.Errorf("failed to generate spec: %s", err)
			}
			fmt.Fprintf(os.Stdout, "# Generated by 'lambdafy spec --version %d %s'\n\n", version, fnName)
			return s.Save(os.Stdout)
		},
	}
	addVersionFlag(specCmd.Flags(), &ver)
}

func generateSpec(fnName string, fnVersion int) (fnspec.Spec, error) {

	spec := fnspec.Spec{}

	ctx := context.Background()
	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return spec, fmt.Errorf("failed to load aws config: %s", err)
	}
	lambdaCl := lambda.NewFromConfig(acfg)

	gfo, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: &fnName,
		Qualifier:    aws.String(strconv.Itoa(fnVersion)),
	})
	if err != nil {
		return spec, err
	}

	if gfo.Code.ImageUri == nil {
		return spec, fmt.Errorf("function %s is not an docker image function", fnName)
	}

	spec.Name = fnName
	spec.Description = *gfo.Configuration.Description
	spec.Image = *gfo.Code.ImageUri
	spec.Role = *gfo.Configuration.Role
	if env := gfo.Configuration.Environment; env != nil {
		spec.Env = env.Variables
	}
	if icr := gfo.Configuration.ImageConfigResponse; icr != nil {
		if imc := icr.ImageConfig; imc != nil {
			spec.Entrypoint = imc.EntryPoint
			spec.Command = imc.Command
			spec.WorkDir = imc.WorkingDirectory
		}
	}
	spec.Memory = gfo.Configuration.MemorySize
	spec.Timeout = gfo.Configuration.Timeout
	spec.Tags = gfo.Tags
	spec.VPCSecurityGroupIds = gfo.Configuration.VpcConfig.SecurityGroupIds
	sort.StringSlice(spec.VPCSecurityGroupIds).Sort()
	spec.VPCSubnetIds = gfo.Configuration.VpcConfig.SubnetIds
	sort.StringSlice(spec.VPCSubnetIds).Sort()
	for _, fsc := range gfo.Configuration.FileSystemConfigs {
		spec.EFSMounts = append(spec.EFSMounts, fnspec.EFSMount{
			ARN:  *fsc.Arn,
			Path: *fsc.LocalMountPath,
		})
	}
	spec.TempSize = gfo.Configuration.EphemeralStorage.Size

	// Derive allowed account regions from current account and region.

	stsCl := sts.NewFromConfig(acfg)
	ident, err := stsCl.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return spec, fmt.Errorf("failed to get caller identity: %s", err)
	}
	spec.AllowedAccountRegions = []string{fmt.Sprintf("%s:%s", *ident.Account, acfg.Region)}

	// Determine if role was generated by us.

	if err := func() error {
		roleName := spec.Role[strings.LastIndex(spec.Role, "/")+1:]
		if !strings.HasPrefix(roleName, generatedRolePrefix) {
			return nil
		}
		chksum := roleName[len(generatedRolePrefix):]
		iamCl := iam.NewFromConfig(acfg)
		r, err := iamCl.GetRole(ctx, &iam.GetRoleInput{
			RoleName: &roleName,
		})
		if err != nil {
			return fmt.Errorf("failed to get role: %s", err)
		}
		if r.Role.AssumeRolePolicyDocument != &defaultAssumeRolePolicy {
			return nil
		}
		p, err := iamCl.GetRolePolicy(ctx, &iam.GetRolePolicyInput{
			RoleName:   &roleName,
			PolicyName: aws.String("main"),
		})
		if err != nil {
			if strings.Contains(err.Error(), "NoSuchEntity") {
				return nil
			}
			return fmt.Errorf("failed to get role policy: %s", err)
		}
		if fmt.Sprintf("%x", md5.Sum([]byte(*p.PolicyDocument))) != chksum {
			return nil
		}

		policies := struct {
			Statement []fnspec.RolePolicy
		}{}
		if err := json.NewDecoder(strings.NewReader(*p.PolicyDocument)).Decode(&policies); err != nil {
			return fmt.Errorf("failed to decode role policy: %s", err)
		}
		spec.Role = "generate"
		spec.RoleExtraPolicy = policies.Statement[1:] // The first one is the default one we add.
		return nil
	}(); err != nil {
		return spec, err
	}

	return spec, nil
}
