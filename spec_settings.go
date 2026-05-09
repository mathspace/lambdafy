package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mathspace/lambdafy/fnspec"
)

func logGroupRetentionDaysFromSpecEnv(env map[string]string) (int32, error) {
	if env == nil {
		return fnspec.DefaultLogGroupRetentionDays, nil
	}

	raw, ok := env[specInEnvLogGroupRetentionDays]
	if !ok {
		return fnspec.DefaultLogGroupRetentionDays, nil
	}

	raw = strings.TrimSpace(raw)
	days, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value %q: %w", specInEnvLogGroupRetentionDays, raw, err)
	}

	retentionDays := int32(days)
	if !fnspec.IsValidLogGroupRetentionDays(retentionDays) {
		return 0, fmt.Errorf("invalid %s value %q", specInEnvLogGroupRetentionDays, raw)
	}

	return retentionDays, nil
}
