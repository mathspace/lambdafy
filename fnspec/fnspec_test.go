package fnspec

import (
	"strings"
	"testing"
)

func TestLoadRequiresExistingRoleName(t *testing.T) {
	tests := []struct {
		name    string
		role    string
		wantErr string
	}{
		{
			name:    "generate rejected",
			role:    "generate",
			wantErr: "role: generate is no longer supported",
		},
		{
			name:    "arn rejected",
			role:    "arn:aws:iam::123456789012:role/my-function",
			wantErr: "role must be an existing IAM role name",
		},
		{
			name:    "path rejected",
			role:    "service/my-function",
			wantErr: "role must be an existing IAM role name",
		},
		{
			name: "name accepted",
			role: "my-function_role+=,.@-",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(strings.NewReader(minimalSpec(test.role)), nil)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Load returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Load returned nil error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Load error = %q, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestLoadRejectsRoleExtraPolicy(t *testing.T) {
	spec := minimalSpec("my-function") + `
role_extra_policy:
  - effect: Allow
    action:
      - s3:GetObject
    resource:
      - "*"
`
	_, err := Load(strings.NewReader(spec), nil)
	if err == nil {
		t.Fatalf("Load returned nil error")
	}
	if !strings.Contains(err.Error(), "role_extra_policy is no longer supported") {
		t.Fatalf("Load error = %q", err)
	}
}

func minimalSpec(role string) string {
	return `
name: my-function
image: 123456789012.dkr.ecr.us-east-1.amazonaws.com/my-function:latest
role: ` + role + `
`
}
