package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/mathspace/lambdafy/fnspec"
)

const (
	lambdaServicePrincipal    = "lambda.amazonaws.com"
	lambdaSourceFunctionARN   = "lambda:SourceFunctionArn"
	schedulerServicePrincipal = "scheduler.amazonaws.com"
)

type iamRoleValidationClient interface {
	GetRole(context.Context, *iam.GetRoleInput, ...func(*iam.Options)) (*iam.GetRoleOutput, error)
	SimulatePrincipalPolicy(context.Context, *iam.SimulatePrincipalPolicyInput, ...func(*iam.Options)) (*iam.SimulatePrincipalPolicyOutput, error)
}

type requiredRolePermission struct {
	Action                  string
	Resource                string
	LambdaSourceFunctionARN string
}

type missingRolePermission struct {
	requiredRolePermission
	Decision             iamtypes.PolicyEvaluationDecisionType
	MissingContextValues []string
}

type roleValidationScope struct {
	LambdaSourceFunctionARN string
	SchedulerTargetARN      string
}

func resolveAndValidateRole(ctx context.Context, iamCl iamRoleValidationClient, spec *fnspec.Spec, accountID, region string, scope roleValidationScope) (string, error) {
	role, err := iamCl.GetRole(ctx, &iam.GetRoleInput{
		RoleName: aws.String(spec.Role),
	})
	if err != nil {
		return "", fmt.Errorf("failed to lookup role %q: %w", spec.Role, err)
	}
	if role.Role == nil || role.Role.Arn == nil || *role.Role.Arn == "" {
		return "", fmt.Errorf("role %q lookup returned no ARN", spec.Role)
	}
	if role.Role.AssumeRolePolicyDocument == nil || *role.Role.AssumeRolePolicyDocument == "" {
		return "", fmt.Errorf("role %q has no assume role policy", spec.Role)
	}

	servicePrincipals := []string{lambdaServicePrincipal}
	if len(spec.CronTriggers) > 0 {
		servicePrincipals = append(servicePrincipals, schedulerServicePrincipal)
	}
	if err := validateAssumeRolePolicy(*role.Role.AssumeRolePolicyDocument, servicePrincipals); err != nil {
		return "", fmt.Errorf("role %q cannot be used by lambdafy: %w", spec.Role, err)
	}

	permissions, err := requiredExecutionRolePermissions(spec, accountID, region, scope)
	if err != nil {
		return "", err
	}
	missing, err := missingRequiredRolePermissions(ctx, iamCl, *role.Role.Arn, permissions)
	if err != nil {
		return "", fmt.Errorf("failed to simulate role %q permissions: %w", spec.Role, err)
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("role %q is missing required permissions: %s", spec.Role, formatMissingRolePermissions(missing))
	}

	return *role.Role.Arn, nil
}

func requiredExecutionRolePermissions(spec *fnspec.Spec, accountID, region string, scope roleValidationScope) ([]requiredRolePermission, error) {
	logGroupARN := fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s", region, accountID, lambdaLogGroupName(spec.Name))
	logStreamARN := logGroupARN + ":log-stream:*"
	// Lambdafy manages the log group before deploy-time invocation, so the
	// execution role only needs stream/event permissions for Lambda logging.
	// Lambda provides lambda:SourceFunctionArn for log delivery and calls made
	// inside the runtime, but not for ENI setup or SQS event-source polling.
	permissions := []requiredRolePermission{
		{Action: "logs:CreateLogStream", Resource: logStreamARN, LambdaSourceFunctionARN: scope.LambdaSourceFunctionARN},
		{Action: "logs:PutLogEvents", Resource: logStreamARN, LambdaSourceFunctionARN: scope.LambdaSourceFunctionARN},
	}

	if len(spec.VPCSecurityGroupIds) > 0 || len(spec.VPCSubnetIds) > 0 {
		permissions = append(permissions,
			requiredRolePermission{Action: "ec2:AssignPrivateIpAddresses", Resource: "*"},
			requiredRolePermission{Action: "ec2:CreateNetworkInterface", Resource: "*"},
			requiredRolePermission{Action: "ec2:DeleteNetworkInterface", Resource: "*"},
			requiredRolePermission{Action: "ec2:DescribeNetworkInterfaces", Resource: "*"},
			requiredRolePermission{Action: "ec2:UnassignPrivateIpAddresses", Resource: "*"},
		)
	}

	for _, trigger := range spec.SQSTriggers {
		permissions = append(permissions,
			requiredRolePermission{Action: "sqs:DeleteMessage", Resource: trigger.ARN},
			requiredRolePermission{Action: "sqs:GetQueueAttributes", Resource: trigger.ARN},
			requiredRolePermission{Action: "sqs:ReceiveMessage", Resource: trigger.ARN},
		)
	}
	for _, arn := range sqsSendQueueARNs(spec.Env) {
		permissions = append(permissions,
			requiredRolePermission{Action: "sqs:SendMessage", Resource: arn, LambdaSourceFunctionARN: scope.LambdaSourceFunctionARN},
			requiredRolePermission{Action: "sqs:SendMessageBatch", Resource: arn, LambdaSourceFunctionARN: scope.LambdaSourceFunctionARN},
		)
	}

	if len(spec.CronTriggers) > 0 && scope.SchedulerTargetARN != "" {
		permissions = append(permissions, requiredRolePermission{
			Action:   "lambda:InvokeFunction",
			Resource: scope.SchedulerTargetARN,
		})
	}

	return dedupeRequiredRolePermissions(permissions), nil
}

func lambdaFunctionARN(accountID, region, fnName string) string {
	return fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s", region, accountID, fnName)
}

func unqualifiedLambdaFunctionARN(functionARN string) string {
	parts := strings.Split(functionARN, ":")
	if len(parts) > 7 && parts[5] == "function" {
		return strings.Join(parts[:7], ":")
	}
	return functionARN
}

func sqsSendQueueARNs(env map[string]string) []string {
	const prefix = "*lambdafy_sqs_send:"

	arns := make([]string, 0)
	for _, value := range env {
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		arns = append(arns, strings.TrimSpace(strings.TrimPrefix(value, prefix)))
	}
	sort.Strings(arns)
	return arns
}

func dedupeRequiredRolePermissions(permissions []requiredRolePermission) []requiredRolePermission {
	seen := make(map[requiredRolePermission]bool, len(permissions))
	unique := make([]requiredRolePermission, 0, len(permissions))
	for _, permission := range permissions {
		if permission.Action == "" || permission.Resource == "" || seen[permission] {
			continue
		}
		seen[permission] = true
		unique = append(unique, permission)
	}
	sort.Slice(unique, func(i, j int) bool {
		if unique[i].Action == unique[j].Action {
			return unique[i].Resource < unique[j].Resource
		}
		return unique[i].Action < unique[j].Action
	})
	return unique
}

func missingRequiredRolePermissions(ctx context.Context, iamCl iamRoleValidationClient, roleARN string, permissions []requiredRolePermission) ([]missingRolePermission, error) {
	missing := make([]missingRolePermission, 0)
	for _, permission := range permissions {
		decision, missingContextValues, err := simulateRolePermission(ctx, iamCl, roleARN, permission)
		if err != nil {
			return nil, err
		}
		if decision != iamtypes.PolicyEvaluationDecisionTypeAllowed {
			missing = append(missing, missingRolePermission{
				requiredRolePermission: permission,
				Decision:               decision,
				MissingContextValues:   missingContextValues,
			})
		}
	}
	return missing, nil
}

func simulateRolePermission(ctx context.Context, iamCl iamRoleValidationClient, roleARN string, permission requiredRolePermission) (iamtypes.PolicyEvaluationDecisionType, []string, error) {
	in := &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: aws.String(roleARN),
		ActionNames:     []string{permission.Action},
		ResourceArns:    []string{permission.Resource},
	}
	if permission.LambdaSourceFunctionARN != "" {
		in.ContextEntries = []iamtypes.ContextEntry{
			{
				ContextKeyName:   aws.String(lambdaSourceFunctionARN),
				ContextKeyType:   iamtypes.ContextKeyTypeEnumString,
				ContextKeyValues: []string{permission.LambdaSourceFunctionARN},
			},
		}
	}
	paginator := iam.NewSimulatePrincipalPolicyPaginator(iamCl, in)

	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			return "", nil, err
		}
		for _, result := range out.EvaluationResults {
			if result.EvalActionName == nil || !strings.EqualFold(*result.EvalActionName, permission.Action) {
				continue
			}
			for _, resourceResult := range result.ResourceSpecificResults {
				if resourceResult.EvalResourceName == nil || *resourceResult.EvalResourceName != permission.Resource {
					continue
				}
				return resourceResult.EvalResourceDecision, resourceResult.MissingContextValues, nil
			}
			if len(result.ResourceSpecificResults) > 0 {
				continue
			}
			return result.EvalDecision, result.MissingContextValues, nil
		}
	}

	return iamtypes.PolicyEvaluationDecisionTypeImplicitDeny, nil, nil
}

func formatMissingRolePermissions(missing []missingRolePermission) string {
	parts := make([]string, 0, len(missing))
	for _, permission := range missing {
		msg := fmt.Sprintf("%s on %s (%s)", permission.Action, permission.Resource, permission.Decision)
		if len(permission.MissingContextValues) > 0 {
			msg += fmt.Sprintf(", missing context: %s", strings.Join(permission.MissingContextValues, ", "))
		}
		parts = append(parts, msg)
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

type assumeRolePolicy struct {
	Statement assumeRoleStatements `json:"Statement"`
}

type assumeRoleStatements []assumeRoleStatement

func (s *assumeRoleStatements) UnmarshalJSON(b []byte) error {
	var statements []assumeRoleStatement
	if err := json.Unmarshal(b, &statements); err == nil {
		*s = statements
		return nil
	}

	var statement assumeRoleStatement
	if err := json.Unmarshal(b, &statement); err != nil {
		return err
	}
	*s = []assumeRoleStatement{statement}
	return nil
}

type assumeRoleStatement struct {
	Effect    string              `json:"Effect"`
	Action    stringList          `json:"Action"`
	Principal assumeRolePrincipal `json:"Principal"`
}

type assumeRolePrincipal struct {
	All      bool
	Services stringList
}

func (p *assumeRolePrincipal) UnmarshalJSON(b []byte) error {
	var all string
	if err := json.Unmarshal(b, &all); err == nil {
		p.All = all == "*"
		return nil
	}

	var principal struct {
		Service stringList `json:"Service"`
	}
	if err := json.Unmarshal(b, &principal); err != nil {
		return err
	}
	p.Services = principal.Service
	return nil
}

func (p assumeRolePrincipal) allowsService(service string) bool {
	if p.All {
		return true
	}
	for _, candidate := range p.Services {
		if candidate == "*" || strings.EqualFold(candidate, service) {
			return true
		}
	}
	return false
}

type stringList []string

func (s *stringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*s = []string{one}
		return nil
	}

	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

func (s stringList) matchesAction(action string) bool {
	action = strings.ToLower(action)
	for _, candidate := range s {
		candidate = strings.ToLower(candidate)
		switch {
		case candidate == "*":
			return true
		case strings.HasSuffix(candidate, "*") && strings.HasPrefix(action, strings.TrimSuffix(candidate, "*")):
			return true
		case candidate == action:
			return true
		}
	}
	return false
}

func validateAssumeRolePolicy(encodedPolicy string, servicePrincipals []string) error {
	policyJSON, err := url.QueryUnescape(encodedPolicy)
	if err != nil {
		return fmt.Errorf("failed to decode assume role policy: %w", err)
	}

	var policy assumeRolePolicy
	if err := json.Unmarshal([]byte(policyJSON), &policy); err != nil {
		return fmt.Errorf("failed to parse assume role policy: %w", err)
	}

	for _, service := range servicePrincipals {
		if !assumeRolePolicyAllowsService(policy, service) {
			return fmt.Errorf("assume role policy must allow %s to call sts:AssumeRole", service)
		}
	}
	return nil
}

func assumeRolePolicyAllowsService(policy assumeRolePolicy, service string) bool {
	for _, statement := range policy.Statement {
		if !strings.EqualFold(statement.Effect, "Allow") {
			continue
		}
		if !statement.Action.matchesAction("sts:AssumeRole") {
			continue
		}
		if !statement.Principal.allowsService(service) {
			continue
		}
		return true
	}
	return false
}
