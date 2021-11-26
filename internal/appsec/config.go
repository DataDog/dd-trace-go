// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the AppSec configuration.
type Config struct {
	// MaxBatchLen is the maximum batch length the event batching loop should use. The event batch is sent when
	// this length is reached. Defaults to 1024.
	MaxBatchLen int
	// MaxBatchStaleTime is the maximum amount of time events are kept in the batch. This allows to send the batch
	// after this amount of time even if the maximum batch length is not reached yet. Defaults to 1 second.
	MaxBatchStaleTime time.Duration

	// rules loaded via the env var DD_APPSEC_RULES. When not set, the builtin rules will be used.
	rules []byte
}

// isEnabled returns true when appsec is enabled when the environment variable
// DD_APPSEC_ENABLED is set to true.
func isEnabled() (bool, error) {
	enabledStr := os.Getenv("DD_APPSEC_ENABLED")
	if enabledStr == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(enabledStr)
	if err != nil {
		return false, fmt.Errorf("could not parse DD_APPSEC_ENABLED value `%s` as a boolean value", enabledStr)
	}
	return enabled, nil
}
