package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/mathspace/lambdafy/fnspec"
)

const (
	testAccountID = "123456789012"
	testRegion    = "us-east-1"
	testRoleARN   = "arn:aws:iam::123456789012:role/my-function-role"
)

func TestResolveAndValidateRoleAllowsConfiguredRole(t *testing.T) {
	spec := &fnspec.Spec{
		Name: "my-function",
		Role: "my-function-role",
		Env:  map[string]string{},
	}
	scope := roleValidationScope{}
	permissions, err := requiredExecutionRolePermissions(spec, testAccountID, testRegion, scope)
	if err != nil {
		t.Fatal(err)
	}
	fake := newFakeRoleValidationClient(trustPolicy(lambdaServicePrincipal), permissions)

	roleARN, err := resolveAndValidateRole(context.Background(), fake, spec, testAccountID, testRegion, scope)
	if err != nil {
		t.Fatalf("resolveAndValidateRole returned error: %v", err)
	}
	if roleARN != testRoleARN {
		t.Fatalf("role ARN = %q, want %q", roleARN, testRoleARN)
	}
	if len(fake.simulated) != len(permissions) {
		t.Fatalf("simulated %d permissions, want %d", len(fake.simulated), len(permissions))
	}
}

func TestResolveAndValidateRoleFailsWhenPermissionIsMissing(t *testing.T) {
	spec := &fnspec.Spec{
		Name: "my-function",
		Role: "my-function-role",
		Env:  map[string]string{},
	}
	scope := roleValidationScope{}
	permissions, err := requiredExecutionRolePermissions(spec, testAccountID, testRegion, scope)
	if err != nil {
		t.Fatal(err)
	}
	allowed := withoutPermission(permissions, requiredRolePermission{
		Action:   "logs:PutLogEvents",
		Resource: fmt.Sprintf("arn:aws:logs:%s:%s:log-group:/aws/lambda/my-function:log-stream:*", testRegion, testAccountID),
	})
	fake := newFakeRoleValidationClient(trustPolicy(lambdaServicePrincipal), allowed)

	_, err = resolveAndValidateRole(context.Background(), fake, spec, testAccountID, testRegion, scope)
	if err == nil {
		t.Fatalf("resolveAndValidateRole returned nil error")
	}
	if !strings.Contains(err.Error(), "logs:PutLogEvents") {
		t.Fatalf("error = %q, want missing logs:PutLogEvents", err)
	}
}

func TestRequiredExecutionRolePermissionsOmitsManagedLogGroupCreation(t *testing.T) {
	spec := &fnspec.Spec{
		Name: "my-function",
		Role: "my-function-role",
		Env:  map[string]string{},
	}

	permissions, err := requiredExecutionRolePermissions(spec, testAccountID, testRegion, roleValidationScope{})
	if err != nil {
		t.Fatal(err)
	}
	if hasPermission(permissions, requiredRolePermission{
		Action:   "logs:CreateLogGroup",
		Resource: fmt.Sprintf("arn:aws:logs:%s:%s:log-group:/aws/lambda/my-function", testRegion, testAccountID),
	}) {
		t.Fatalf("required permissions included managed log group creation: %v", permissions)
	}
}

func TestResolveAndValidateRoleRequiresSchedulerTrustForCron(t *testing.T) {
	spec := &fnspec.Spec{
		Name:         "my-function",
		Role:         "my-function-role",
		Env:          map[string]string{},
		CronTriggers: map[string]string{"hourly": "0 * * * ? *"},
	}
	scope := roleValidationScope{}
	permissions, err := requiredExecutionRolePermissions(spec, testAccountID, testRegion, scope)
	if err != nil {
		t.Fatal(err)
	}
	fake := newFakeRoleValidationClient(trustPolicy(lambdaServicePrincipal), permissions)

	_, err = resolveAndValidateRole(context.Background(), fake, spec, testAccountID, testRegion, scope)
	if err == nil {
		t.Fatalf("resolveAndValidateRole returned nil error")
	}
	if !strings.Contains(err.Error(), schedulerServicePrincipal) {
		t.Fatalf("error = %q, want missing scheduler trust", err)
	}
	if len(fake.simulated) != 0 {
		t.Fatalf("simulated permissions after trust failure: %v", fake.simulated)
	}
}

func TestResolveAndValidateRoleValidatesCronInvokeAgainstSchedulerTargetARN(t *testing.T) {
	spec := &fnspec.Spec{
		Name:         "my-function",
		Role:         "my-function-role",
		Env:          map[string]string{},
		CronTriggers: map[string]string{"hourly": "0 * * * ? *"},
	}
	targetARN := "arn:aws:lambda:us-east-1:123456789012:function:my-function:42"
	scope := roleValidationScope{SchedulerTargetARN: targetARN}
	permissions, err := requiredExecutionRolePermissions(spec, testAccountID, testRegion, scope)
	if err != nil {
		t.Fatal(err)
	}
	fake := newFakeRoleValidationClient(trustPolicy(lambdaServicePrincipal, schedulerServicePrincipal), permissions)

	_, err = resolveAndValidateRole(context.Background(), fake, spec, testAccountID, testRegion, scope)
	if err != nil {
		t.Fatalf("resolveAndValidateRole returned error: %v", err)
	}
	if !hasPermission(fake.simulated, requiredRolePermission{
		Action:   "lambda:InvokeFunction",
		Resource: targetARN,
	}) {
		t.Fatalf("simulated permissions did not include target version invoke: %v", fake.simulated)
	}
}

func TestResolveAndValidateRoleProvidesLambdaSourceFunctionContext(t *testing.T) {
	sourceARN := "arn:aws:lambda:us-east-1:123456789012:function:my-function"
	queueARN := "arn:aws:sqs:us-east-1:123456789012:queue"
	spec := &fnspec.Spec{
		Name: "my-function",
		Role: "my-function-role",
		Env: map[string]string{
			"SEND_URL": "*lambdafy_sqs_send:" + queueARN,
		},
	}
	scope := roleValidationScope{LambdaSourceFunctionARN: sourceARN}
	permissions, err := requiredExecutionRolePermissions(spec, testAccountID, testRegion, scope)
	if err != nil {
		t.Fatal(err)
	}
	fake := newFakeRoleValidationClient(trustPolicy(lambdaServicePrincipal), permissions)

	_, err = resolveAndValidateRole(context.Background(), fake, spec, testAccountID, testRegion, scope)
	if err != nil {
		t.Fatalf("resolveAndValidateRole returned error: %v", err)
	}
	if !hasPermission(fake.simulated, requiredRolePermission{
		Action:                  "sqs:SendMessage",
		Resource:                queueARN,
		LambdaSourceFunctionARN: sourceARN,
	}) {
		t.Fatalf("simulated permissions did not include lambda source context: %v", fake.simulated)
	}
}

func TestRequiredExecutionRolePermissionsOmitsLambdaSourceContextForEventSourcePolling(t *testing.T) {
	sourceARN := "arn:aws:lambda:us-east-1:123456789012:function:my-function"
	queueARN := "arn:aws:sqs:us-east-1:123456789012:queue"
	spec := &fnspec.Spec{
		Name: "my-function",
		Role: "my-function-role",
		Env:  map[string]string{},
		SQSTriggers: []*fnspec.SQSTrigger{
			{ARN: queueARN},
		},
	}

	permissions, err := requiredExecutionRolePermissions(spec, testAccountID, testRegion, roleValidationScope{
		LambdaSourceFunctionARN: sourceARN,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPermission(permissions, requiredRolePermission{
		Action:   "sqs:ReceiveMessage",
		Resource: queueARN,
	}) {
		t.Fatalf("required permissions did not include SQS poll permission without lambda source context: %v", permissions)
	}
	if hasPermission(permissions, requiredRolePermission{
		Action:                  "sqs:ReceiveMessage",
		Resource:                queueARN,
		LambdaSourceFunctionARN: sourceARN,
	}) {
		t.Fatalf("SQS poll permission included lambda source context: %v", permissions)
	}
}

func TestRequiredExecutionRolePermissionsIncludesSQSBatchSend(t *testing.T) {
	spec := &fnspec.Spec{
		Name: "my-function",
		Role: "my-function-role",
		Env: map[string]string{
			"SEND_URL": "*lambdafy_sqs_send:arn:aws:sqs:us-east-1:123456789012:queue",
		},
	}

	permissions, err := requiredExecutionRolePermissions(spec, testAccountID, testRegion, roleValidationScope{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPermission(permissions, requiredRolePermission{
		Action:   "sqs:SendMessageBatch",
		Resource: "arn:aws:sqs:us-east-1:123456789012:queue",
	}) {
		t.Fatalf("required permissions did not include sqs:SendMessageBatch: %v", permissions)
	}
}

func TestUnqualifiedLambdaFunctionARN(t *testing.T) {
	got := unqualifiedLambdaFunctionARN("arn:aws:lambda:us-east-1:123456789012:function:my-function:42")
	want := "arn:aws:lambda:us-east-1:123456789012:function:my-function"
	if got != want {
		t.Fatalf("unqualifiedLambdaFunctionARN() = %q, want %q", got, want)
	}
}

type fakeRoleValidationClient struct {
	trustPolicy string
	allowed     map[requiredRolePermission]bool
	simulated   []requiredRolePermission
}

func newFakeRoleValidationClient(trustPolicy string, allowed []requiredRolePermission) *fakeRoleValidationClient {
	allowedMap := make(map[requiredRolePermission]bool, len(allowed))
	for _, permission := range allowed {
		allowedMap[permission] = true
	}
	return &fakeRoleValidationClient{
		trustPolicy: trustPolicy,
		allowed:     allowedMap,
	}
}

func (f *fakeRoleValidationClient) GetRole(context.Context, *iam.GetRoleInput, ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	return &iam.GetRoleOutput{
		Role: &iamtypes.Role{
			Arn:                      aws.String(testRoleARN),
			AssumeRolePolicyDocument: aws.String(f.trustPolicy),
		},
	}, nil
}

func (f *fakeRoleValidationClient) SimulatePrincipalPolicy(_ context.Context, input *iam.SimulatePrincipalPolicyInput, _ ...func(*iam.Options)) (*iam.SimulatePrincipalPolicyOutput, error) {
	permission := requiredRolePermission{
		Action:                  input.ActionNames[0],
		Resource:                input.ResourceArns[0],
		LambdaSourceFunctionARN: lambdaSourceFunctionARNFromContext(input.ContextEntries),
	}
	f.simulated = append(f.simulated, permission)

	decision := iamtypes.PolicyEvaluationDecisionTypeImplicitDeny
	if f.allowed[permission] {
		decision = iamtypes.PolicyEvaluationDecisionTypeAllowed
	}

	return &iam.SimulatePrincipalPolicyOutput{
		EvaluationResults: []iamtypes.EvaluationResult{
			{
				EvalActionName: aws.String(permission.Action),
				ResourceSpecificResults: []iamtypes.ResourceSpecificResult{
					{
						EvalResourceDecision: decision,
						EvalResourceName:     aws.String(permission.Resource),
					},
				},
			},
		},
	}, nil
}

func lambdaSourceFunctionARNFromContext(entries []iamtypes.ContextEntry) string {
	for _, entry := range entries {
		if entry.ContextKeyName == nil || *entry.ContextKeyName != lambdaSourceFunctionARN {
			continue
		}
		if len(entry.ContextKeyValues) == 0 {
			return ""
		}
		return entry.ContextKeyValues[0]
	}
	return ""
}

func trustPolicy(services ...string) string {
	quoted := make([]string, 0, len(services))
	for _, service := range services {
		quoted = append(quoted, fmt.Sprintf("%q", service))
	}
	return fmt.Sprintf(`{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "sts:AssumeRole",
      "Principal": {
        "Service": [%s]
      }
    }
  ]
}`, strings.Join(quoted, ","))
}

func withoutPermission(permissions []requiredRolePermission, excluded requiredRolePermission) []requiredRolePermission {
	filtered := make([]requiredRolePermission, 0, len(permissions))
	for _, permission := range permissions {
		if permission == excluded {
			continue
		}
		filtered = append(filtered, permission)
	}
	return filtered
}

func hasPermission(permissions []requiredRolePermission, needle requiredRolePermission) bool {
	for _, permission := range permissions {
		if permission == needle {
			return true
		}
	}
	return false
}
