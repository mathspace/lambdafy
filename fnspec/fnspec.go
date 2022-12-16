// Package fnspec provides a function spec for lambdafy.
package fnspec

import (
	"errors"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/gobwas/glob"
	"gopkg.in/yaml.v2"
)

var ecrRepoPat = regexp.MustCompile(`^\d+\.dkr\.ecr\.[^.]+\.amazonaws\.com/`)

// EFSMount represents an AWS Elastic Filesystem mount.
type EFSMount struct {
	ARN  string `yaml:"arn"`  // ARN of the EFS filesystem endpoint.
	Path string `yaml:"path"` // Path to mount the EFS filesystem at.
}

// Spec is the specification of a lambda function.
type Spec struct {
	Name                  string            `yaml:"name"`
	Description           string            `yaml:"description,omitempty"`
	Image                 string            `yaml:"image"`
	Role                  string            `yaml:"role"`
	Env                   map[string]string `yaml:"env,omitempty"`
	Entrypoint            []string          `yaml:"entrypoint,omitempty"`
	Command               []string          `yaml:"command,omitempty"`
	WorkDir               *string           `yaml:"workdir,omitempty"`
	Memory                *int32            `yaml:"memory,omitempty"`
	Timeout               *int32            `yaml:"timeout,omitempty"`
	Tags                  map[string]string `yaml:"tags,omitempty"`
	VPCSecurityGroupIds   []string          `yaml:"vpc_security_group_ids,omitempty"`
	VPCSubnetIds          []string          `yaml:"vpc_subnet_ids,omitempty"`
	EFSMounts             []EFSMount        `yaml:"efs_mounts,omitempty"`
	TempSize              *int32            `yaml:"temp_size,omitempty"`
	AllowedAccountRegions []string          `yaml:"allowed_account_regions,omitempty"`
	allowedGlobs          []glob.Glob       `yaml:"-"`
}

// IsAccountRegionAllowed returns true if the given account and region are
// allowed by the spec.
func (a *Spec) IsAccountRegionAllowed(account, region string) bool {
	if len(a.allowedGlobs) == 0 {
		return true
	}
	accReg := account + ":" + region
	for _, g := range a.allowedGlobs {
		if g.Match(accReg) {
			return true
		}
	}
	return false
}

// Load loads the spec from the given reader.
func Load(r io.Reader) (*Spec, error) {
	var s Spec
	if err := yaml.NewDecoder(r).Decode(&s); err != nil {
		return nil, err
	}
	if s.Name == "" || s.Image == "" || s.Role == "" {
		return nil, errors.New("name, image and role must be specified")
	}
	if s.Memory != nil && (*s.Memory < 128 || *s.Memory > 10240) {
		return nil, errors.New("memory must be between 128 and 10240 MB")
	}
	if s.Timeout != nil && (*s.Timeout < 3 || *s.Timeout > 900) {
		return nil, errors.New("timeout spec must be between 3 and 900")
	}
	if s.TempSize != nil && (*s.TempSize < 512 || *s.TempSize > 10240) {
		return nil, errors.New("temp_size spec must be between 512 and 10240")
	}
	for _, a := range s.AllowedAccountRegions {
		g, err := glob.Compile(a, ':')
		if err != nil {
			return nil, errors.New("invalid allowed_account_regions pattern")
		}
		s.allowedGlobs = append(s.allowedGlobs, g)
	}
	if !ecrRepoPat.MatchString(s.Image) {
		return nil, errors.New("image must be an ECR repository")
	}
	if !strings.Contains(s.Image, ":") {
		s.Image += ":latest"
	}
	return &s, nil
}

// Save saves the spec to the given writer.
func (a *Spec) Save(w io.Writer) error {
	return yaml.NewEncoder(w).Encode(a)
}

// LoadFromFile loads the spec from the given path.
func LoadFromFile(path string) (*Spec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Load(f)
}
