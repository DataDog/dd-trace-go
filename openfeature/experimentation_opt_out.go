// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/errortrace"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

// ExperimentationOptOutMode controls per-subject exclusion from experimentation.
type ExperimentationOptOutMode int

const (
	// ExperimentationOptOutDefault uses DD_FEATURE_FLAGS_EXPERIMENTATION_OPT_OUT_MODE.
	// If the setting is absent or empty, experimentation opt-out is disabled.
	ExperimentationOptOutDefault ExperimentationOptOutMode = iota
	// ExperimentationOptOutDisabled ignores dd_experimentation_opt_out_at.
	// It overrides the environment setting.
	ExperimentationOptOutDisabled
	// ExperimentationOptOutImmediate skips exposure-logging allocations when
	// dd_experimentation_opt_out_at contains an RFC3339 timestamp string.
	// It applies on each evaluation, including to experiments already in progress.
	// The timestamp records the customer's preference; it does not schedule exclusion.
	ExperimentationOptOutImmediate
)

const experimentationOptOutAtAttribute = "dd_experimentation_opt_out_at"

// String returns a readable name for mode.
func (mode ExperimentationOptOutMode) String() string {
	switch mode {
	case ExperimentationOptOutDefault:
		return "default"
	case ExperimentationOptOutDisabled:
		return "disabled"
	case ExperimentationOptOutImmediate:
		return "immediate"
	default:
		return "ExperimentationOptOutMode(" + strconv.Itoa(int(mode)) + ")"
	}
}

func (mode ExperimentationOptOutMode) resolve() (ExperimentationOptOutMode, error) {
	switch mode {
	case ExperimentationOptOutDisabled, ExperimentationOptOutImmediate:
		return mode, nil
	case ExperimentationOptOutDefault:
		switch strings.ToLower(strings.TrimSpace(internalconfig.Get().FeatureFlagsExperimentationOptOutMode())) {
		case "", "disabled":
			return ExperimentationOptOutDisabled, nil
		case "immediate":
			return ExperimentationOptOutImmediate, nil
		default:
			return mode, errortrace.New("openfeature: DD_FEATURE_FLAGS_EXPERIMENTATION_OPT_OUT_MODE must be disabled or immediate")
		}
	default:
		return mode, errortrace.Errorf("openfeature: unsupported experimentation opt-out mode: %s", mode)
	}
}

func (mode ExperimentationOptOutMode) excludeExperiments(attributes map[string]any) (bool, error) {
	if mode != ExperimentationOptOutImmediate {
		return false, nil
	}
	value, present := attributes[experimentationOptOutAtAttribute]
	if !present {
		return false, nil
	}
	stamp, ok := value.(string)
	if ok {
		if _, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
			return true, nil
		}
	}
	// Reject invalid context before allocation selection; do not infer consent from it.
	return false, errInvalidExperimentationOptOutAt
}

// Build the diagnostic once, without the customer-supplied value.
var errInvalidExperimentationOptOutAt = fmt.Errorf("%w: dd_experimentation_opt_out_at must be an RFC3339 timestamp string; supply a valid timestamp to opt out, or omit the attribute to permit experimentation", errInvalidContext)
