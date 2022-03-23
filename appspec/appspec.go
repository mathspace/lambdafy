package appspec

import (
	"errors"
	"io"
	"os"

	"github.com/gobwas/glob"
	"gopkg.in/yaml.v2"
)

type AppSpec struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Public      bool              `yaml:"public"`
	Env         map[string]string `yaml:"env"`
	Entrypoint  []string          `yaml:"entrypoint"`
	Command     []string          `yaml:"command"`
	WorkDir     *string           `yaml:"workdir"`
	Role        string            `yaml:"role"`
	Memory      *int32            `yaml:"memory"`
	Timeout     *int32            `yaml:"timeout"`

	ReservedConcurrency   *int32   `yaml:"reserved_concurrency"`
	AllowedAccountRegions []string `yaml:"allowed_account_regions"`
	allowedGlobs          []glob.Glob
}

func (a *AppSpec) IsAccountRegionAllowed(account, region string) bool {
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

func Load(r io.Reader) (*AppSpec, error) {
	var s AppSpec
	if err := yaml.NewDecoder(r).Decode(&s); err != nil {
		return nil, err
	}
	if s.Name == "" {
		return nil, errors.New("name spec must be specified")
	}
	if s.Memory != nil && (*s.Memory < 128 || *s.Memory > 10240) {
		return nil, errors.New("memory spec must be between 128 and 10240")
	}
	if s.Timeout != nil && (*s.Timeout < 3 || *s.Timeout > 900) {
		return nil, errors.New("timeout spec must be between 3 and 900")
	}
	if s.ReservedConcurrency != nil && *s.ReservedConcurrency < 1 {
		return nil, errors.New("reserved_concurency spec must be > 0")
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

func LoadFromFile(path string) (*AppSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Load(f)
}
