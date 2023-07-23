package main

import (
	"context"
	"crypto/md5"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/cobra"

	"github.com/mathspace/lambdafy/fnspec"
)

var publishCmd *cobra.Command

var defaultRolePolicyStatements = []*fnspec.RolePolicy{
	{
		Effect: "Allow",
		Action: []string{
			"ec2:AssignPrivateIpAddresses",
			"ec2:CreateNetworkInterface",
			"ec2:DeleteNetworkInterface",
			"ec2:DescribeNetworkInterfaces",
			"ec2:UnassignPrivateIpAddresses",
			"logs:CreateLogGroup",
			"logs:CreateLogStream",
			"logs:PutLogEvents",
			"sqs:DeleteMessage",
			"sqs:GetQueueAttributes",
			"sqs:ReceiveMessage",
			"sqs:SendMessage",
			// This is needed for Amazon Event Bridge Scheduler to call the function.
			"lambda:InvokeFunction", // FIXME too permissive
		},
		Resource: []string{"*"},
	},
}

const (
	// specInEnvPrefix is the prefix for environment variables that encode
	// parts of the function spec that cannot be stored in any other part of a
	// lambda function configuration. For example, the CORS settings are stored in
	// the function URL settings which belongs to function aliases and is only
	// created/updated as part of deploying. Therefore, we need a way to encode
	// the CORS settings in the function config so that we can use reference them
	// when deploying a function.
	specInEnvPrefix = "LAMBDAFY__SPEC_"

	specInEnvCronPrefix = specInEnvPrefix + "CRON_"

	// generatedRolePrefix is the prefix for IAM roles that are generated by
	// lambdafy.
	generatedRolePrefix = "lambdafy-v1-"
)

var defaultAssumeRolePolicy = `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "sts:AssumeRole",
      "Principal": {
        "Service": [
          "lambda.amazonaws.com",
          "scheduler.amazonaws.com"
        ]
      }
    }
  ]
}
`

func init() {
	var al string
	var vars *[]string
	var forceUpdateAlias bool
	var pauseSQSTriggers bool
	publishCmd = &cobra.Command{
		Use:     "publish {spec-file|-}",
		Aliases: []string{"pub"},
		Short:   "Publish a new version of a function without routing traffic to it",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {

			if pauseSQSTriggers {
				return fmt.Errorf("pause-sqs-triggers is not yet implemented")
			}

			p := args[0]
			var r io.Reader
			if p == "-" {
				r = os.Stdin
			} else {
				f, err := os.Open(p)
				if err != nil {
					return fmt.Errorf("failed to open spec file: %s", err)
				}
				defer f.Close()
				r = f
			}

			// Convert vars to map
			varMap := make(map[string]string)
			for _, v := range *vars {
				parts := strings.SplitN(v, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid var: %s", v)
				}
				varMap[parts[0]] = parts[1]
			}

			out, err := publish(r, varMap)
			if err != nil {
				return err
			}
			if al != "" {
				err = alias(out.Name, out.Version, al, forceUpdateAlias)
				if err != nil {
					return fmt.Errorf("failed to create alias: %s", err)
				}
				return formatOutput(struct {
					publishResult
					Alias string `json:"alias"`
				}{
					out, al,
				})
			}
			return formatOutput(out)
		},
	}
	publishCmd.Flags().StringVarP(&al, "alias", "a", "", "Alias to create for the new version")
	publishCmd.Flags().BoolVarP(&forceUpdateAlias, "force-update-alias", "A", false, "Force update the alias if already exists")
	publishCmd.Flags().BoolVar(&pauseSQSTriggers, "pause-sqs-triggers", false, "Do not enable SQS triggers when publishing the function")
	vars = publishCmd.Flags().StringArrayP("var", "v", nil, "Replace placeholders in the spec - e.g. FOO=BAR - can be specified multiple times")
}

// publishResult holds the results of a publish operation.
type publishResult struct {
	ARN     string `json:"arn"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

var roleArnPat = regexp.MustCompile(`^arn:aws:iam::\d+:role/.+`)

// publish publishes the lambda function to AWS.
func publish(specReader io.Reader, vars map[string]string) (res publishResult, err error) {
	spec, err := fnspec.Load(specReader, vars)
	if err != nil {
		return res, fmt.Errorf("failed to load function spec: %s", err)
	}
	res.Name = spec.Name

	// HACK add CORS config to env vars so it can be used when deploying.

	corsBytes, err := json.Marshal(fnspec.CORS{
		Origins: spec.CORS.Origins,
		Methods: spec.CORS.Methods,
		Headers: spec.CORS.Headers,
	})
	if err != nil {
		return res, fmt.Errorf("failed to marshal CORS config: %s", err)
	}
	spec.Env[specInEnvPrefix+"CORS"] = string(corsBytes)

	// HACK embed the cron setting into env vars so they can be used by deploy
	// process to create the schedules. This simply passes the responsility of
	// creating/updating the schedules to the deploy process.

	if spec.CronTriggers != nil && len(spec.CronTriggers) > 0 {
		for k, v := range spec.CronTriggers {
			spec.Env[specInEnvCronPrefix+k] = v
		}
	}

	ctx := context.Background()

	// Setup clients

	acfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to load aws config: %s", err)
	}

	// Is the region allowed by spec?

	stsCl := sts.NewFromConfig(acfg)
	cid, err := stsCl.GetCallerIdentity(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("failed to get aws account number: %s", err)
	}
	if !spec.IsAccountRegionAllowed(*cid.Account, acfg.Region) {
		return res, fmt.Errorf("aws account and/or region is not allowed by spec")
	}

	// If VPC config is specified, ensure that at least one egress rule is specified.

	if len(spec.VPCSecurityGroupIds) > 0 || len(spec.VPCSubnetIds) > 0 {
		hasEgress := false
		hasAllEgress := false

		ec2Cl := ec2.NewFromConfig(acfg)
		sgDetails, err := ec2Cl.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
			GroupIds: spec.VPCSecurityGroupIds,
		})
		if err != nil {
			return res, fmt.Errorf("failed to lookup security groups: %s", err)
		}
		for _, sg := range sgDetails.SecurityGroups {
			for _, rule := range sg.IpPermissionsEgress {
				hasEgress = true
				if rule.IpProtocol != nil && *rule.IpProtocol == "-1" {
					hasAllEgress = true
				}
			}
		}

		if !hasEgress {
			return res, fmt.Errorf("VPC config is set in your spec, but no outbound/egress rules specified")
		}
		if !hasAllEgress {
			log.Printf("warning: VPC config is set in your spec, but no outbound/egress rules allow all traffic - you need this to be able to send logs to Cloudwatch")
		}
	}

	// Prepare to create/update lambda function

	if len(spec.Entrypoint) > 0 && spec.Entrypoint[0] != "/lambdafy-proxy" {
		log.Printf("prefixing entrypoint with '/lambdafy-proxy'")
		spec.Entrypoint = append([]string{"/lambdafy-proxy"}, spec.Entrypoint...)
	}

	// Make and push if necessary

	if spec.MakeAndPush() {
		log.Printf("lambdafying image '%s' and pushing", spec.Image)
		var err error
		if err = lambdafyImage(spec.Image); err != nil {
			return res, fmt.Errorf("failed to lambdafy image: %s", err)
		}
		spec.Image, err = push(spec.Image, spec.RepoName, *spec.CreateRepo)
		if err != nil {
			return res, fmt.Errorf("failed to push image: %s", err)
		}
	}

	var roleArn string
	iamCl := iam.NewFromConfig(acfg)

	if spec.Role == fnspec.RoleGenerate {

		log.Printf("generating role")

		// Convert tags to iamtype tags

		tags := make([]iamtypes.Tag, 0, len(spec.Tags))
		for k, v := range spec.Tags {
			tags = append(tags, iamtypes.Tag{
				Key:   aws.String(k),
				Value: aws.String(v),
			})
		}

		// Serialize policy into JSON string

		pol, err := serializeRolePolicy(spec.RoleExtraPolicy)
		if err != nil {
			return res, fmt.Errorf("failed to serialize role policy: %s", err)
		}
		canPol, _ := canonicalizePolicyString(pol, false)
		roleName := fmt.Sprintf("%s%x", generatedRolePrefix, md5.Sum([]byte(defaultAssumeRolePolicy+canPol)))

		// Create/update role

		out, err := iamCl.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName:                 &roleName,
			Description:              aws.String("lambdafy generated role"),
			AssumeRolePolicyDocument: &defaultAssumeRolePolicy,
			Tags:                     tags,
		})
		if err == nil {
			roleArn = *out.Role.Arn
		} else {
			if !strings.Contains(err.Error(), "EntityAlreadyExists") {
				return res, fmt.Errorf("failed to create role: %s", err)
			}
			out, err := iamCl.GetRole(ctx, &iam.GetRoleInput{RoleName: &roleName})
			if err != nil {
				return res, fmt.Errorf("failed to get role: %s", err)
			}
			roleArn = *out.Role.Arn
		}

		// Set policy

		if _, err := iamCl.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
			RoleName:       &roleName,
			PolicyName:     aws.String("main"),
			PolicyDocument: &canPol,
		}); err != nil {
			return res, fmt.Errorf("failed to set role policy: %s", err)
		}

	} else if roleArnPat.MatchString(spec.Role) {
		roleArn = spec.Role
	} else {

		role, err := iamCl.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(spec.Role)})
		if err != nil {
			return res, fmt.Errorf("failed to lookup role '%s': %s", spec.Role, err)
		}
		roleArn = *role.Role.Arn

	}

	tags := make(map[string]string, len(spec.Tags))
	tags["Name"] = spec.Name
	for k, v := range spec.Tags {
		tags[k] = v
	}

	var vpc *lambdatypes.VpcConfig
	vpc = &lambdatypes.VpcConfig{
		SubnetIds:        spec.VPCSubnetIds,
		SecurityGroupIds: spec.VPCSecurityGroupIds,
	}

	fsConfig := make([]lambdatypes.FileSystemConfig, len(spec.EFSMounts))
	for i, m := range spec.EFSMounts {
		fsConfig[i] = lambdatypes.FileSystemConfig{
			Arn:            aws.String(m.ARN),
			LocalMountPath: aws.String(m.Path),
		}
	}

	lambdaCl := lambda.NewFromConfig(acfg)
	fn, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(spec.Name),
	})
	if err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") {
			return res, fmt.Errorf("failed to lookup function '%s': %s", spec.Name, err)
		}

		log.Printf("creating new function '%s'", spec.Name)

		ctxTo, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if err := retryOnResourceConflict(ctxTo, func() error {
			r, err := lambdaCl.CreateFunction(ctxTo, &lambda.CreateFunctionInput{
				FunctionName:  aws.String(spec.Name),
				Description:   aws.String(spec.Description),
				Role:          &roleArn,
				Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureX8664},
				Environment:   &lambdatypes.Environment{Variables: spec.Env},
				Code: &lambdatypes.FunctionCode{
					ImageUri: aws.String(spec.Image),
				},
				ImageConfig: &lambdatypes.ImageConfig{
					EntryPoint:       spec.Entrypoint,
					Command:          spec.Command,
					WorkingDirectory: spec.WorkDir,
				},
				FileSystemConfigs: fsConfig,
				MemorySize:        spec.Memory,
				PackageType:       lambdatypes.PackageTypeImage,
				Publish:           true,
				Tags:              tags,
				Timeout:           spec.Timeout,
				VpcConfig:         vpc,
			})
			if err != nil {
				return err
			}
			res.ARN = *r.FunctionArn
			res.Version = *r.Version
			return nil
		}); err != nil {
			return res, fmt.Errorf("failed to create function: %s", err)
		}

	} else {

		log.Printf("updating existing function '%s'", spec.Name)

		// Update function config

		ctxTo, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if err := retryOnResourceConflict(ctxTo, func() error {
			_, err := lambdaCl.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
				FunctionName: aws.String(spec.Name),
				Description:  aws.String(spec.Description),
				Role:         &roleArn,
				Environment:  &lambdatypes.Environment{Variables: spec.Env},
				ImageConfig: &lambdatypes.ImageConfig{
					EntryPoint:       spec.Entrypoint,
					Command:          spec.Command,
					WorkingDirectory: spec.WorkDir,
				},
				FileSystemConfigs: fsConfig,
				MemorySize:        spec.Memory,
				Timeout:           spec.Timeout,
				VpcConfig:         vpc,
			})
			return err
		}); err != nil {
			return res, fmt.Errorf("failed to update function config: %s", err)
		}

		// Update function code

		ctxTo, cancel = context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if err := retryOnResourceConflict(ctxTo, func() error {
			r, err := lambdaCl.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
				FunctionName:  aws.String(spec.Name),
				Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureX8664},
				ImageUri:      aws.String(spec.Image),
				Publish:       true,
			})
			if err != nil {
				return err
			}
			res.ARN = *r.FunctionArn
			res.Version = *r.Version
			return nil
		}); err != nil {
			return res, fmt.Errorf("failed to update function code: %s", err)
		}

		// Add SQS triggers

		for _, s := range spec.SQSTriggers {
			var scal *lambdatypes.ScalingConfig
			if s.Concurrency != nil {
				scal = &lambdatypes.ScalingConfig{
					MaximumConcurrency: s.Concurrency,
				}
			}
			if _, err = lambdaCl.CreateEventSourceMapping(ctx, &lambda.CreateEventSourceMappingInput{
				EventSourceArn:                 &s.ARN,
				FunctionName:                   aws.String(fmt.Sprintf("%s:%s", spec.Name, res.Version)),
				BatchSize:                      s.BatchSize,
				MaximumBatchingWindowInSeconds: s.BatchWindow,
				ScalingConfig:                  scal,
				FunctionResponseTypes:          []lambdatypes.FunctionResponseType{lambdatypes.FunctionResponseTypeReportBatchItemFailures},
				Enabled:                        aws.Bool(false),
			}); err != nil {
				return res, fmt.Errorf("failed to add SQS trigger: %s", err)
			}
		}

		// Re-tag the function

		if _, err := lambdaCl.TagResource(ctx, &lambda.TagResourceInput{
			Resource: fn.Configuration.FunctionArn,
			Tags:     tags,
		}); err != nil {
			return res, fmt.Errorf("failed to tag function: %s", err)
		}

		// Untag old tags

		oldTags := []string{}
		for k := range fn.Tags {
			if _, ok := tags[k]; !ok {
				oldTags = append(oldTags, k)
			}
		}

		if len(oldTags) > 0 {
			if _, err := lambdaCl.UntagResource(ctx, &lambda.UntagResourceInput{
				Resource: fn.Configuration.FunctionArn,
				TagKeys:  oldTags,
			}); err != nil {
				return res, fmt.Errorf("failed to remove old tags: %s", err)
			}
		}

	}

	log.Printf("waiting for the new function version to become ready")

	return res, waitOnFunc(ctx, lambdaCl, spec.Name, res.Version)
}

// serializeRolePolicy serializes the role policy statements into a JSON string,
// in the format expected by AWS.
func serializeRolePolicy(extra []*fnspec.RolePolicy) (string, error) {
	var policy []*fnspec.RolePolicy
	policy = append(policy, defaultRolePolicyStatements...)
	policy = append(policy, extra...)
	w := strings.Builder{}
	enc := json.NewEncoder(&w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(struct {
		Version   string
		Statement []*fnspec.RolePolicy
	}{
		Version:   "2012-10-17",
		Statement: policy,
	}); err != nil {
		return "", err
	}
	return w.String(), nil
}
