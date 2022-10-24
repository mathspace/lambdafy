// Package fnspec provides a function spec for lambdafy.
package fnspec

import (
	"errors"
	"io"
	"os"

	"github.com/gobwas/glob"
	"gopkg.in/yaml.v2"
)

// Spec is the specification of a lambda function.
type Spec struct {
	Name                  string            `yaml:"name"`
	Description           string            `yaml:"description"`
	Image                 string            `yaml:"image"`
	Env                   map[string]string `yaml:"env"`
	Entrypoint            []string          `yaml:"entrypoint"`
	Command               []string          `yaml:"command"`
	WorkDir               *string           `yaml:"workdir"`
	Role                  string            `yaml:"role"`
	Memory                *int32            `yaml:"memory"`
	Timeout               *int32            `yaml:"timeout"`
	ReservedConcurrency   *int32            `yaml:"reserved_concurrency"`
	AllowedAccountRegions []string          `yaml:"allowed_account_regions"`
	allowedGlobs          []glob.Glob
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
	if s.Name == "" || s.Image == "" {
		return nil, errors.New("name and image must be specified")
	}
	if s.Memory != nil && (*s.Memory < 128 || *s.Memory > 10240) {
		return nil, errors.New("memory must be between 128 and 10240 MB")
	}
	if s.ReservedConcurrency != nil && *s.ReservedConcurrency < 1 {
		return nil, errors.New("reserved_concurency must be > 0")
	}
	if s.Timeout != nil && (*s.Timeout < 3 || *s.Timeout > 900) {
		return nil, errors.New("timeout spec must be between 3 and 900")
	}
	for _, a := range s.AllowedAccountRegions {
		g, err := glob.Compile(a, ':')
		if err != nil {
			return nil, errors.New("invalid allowed_account_regions pattern")
		}
		s.allowedGlobs = append(s.allowedGlobs, g)
	}
	return &s, nil
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
