// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"fmt"
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
	// ExperimentationOptOutFutureExperimentsOnly skips exposure-logging allocations
	// activated at or after dd_experimentation_opt_out_at. Allocations without
	// activation metadata are also skipped. Older allocations evaluate normally.
	// This is an activation cutoff, not a record of the subject's prior enrollment.
	ExperimentationOptOutFutureExperimentsOnly
)

const experimentationOptOutAtAttribute = "dd_experimentation_opt_out_at"

func (mode ExperimentationOptOutMode) resolve() (ExperimentationOptOutMode, error) {
	switch mode {
	case ExperimentationOptOutDisabled, ExperimentationOptOutImmediate, ExperimentationOptOutFutureExperimentsOnly:
		return mode, nil
	case ExperimentationOptOutDefault:
		switch strings.ToLower(strings.TrimSpace(internalconfig.Get().FeatureFlagsExperimentationOptOutMode())) {
		case "", "disabled":
			return ExperimentationOptOutDisabled, nil
		case "immediate":
			return ExperimentationOptOutImmediate, nil
		case "future_experiments_only":
			return ExperimentationOptOutFutureExperimentsOnly, nil
		default:
			return mode, errortrace.New("openfeature: DD_FEATURE_FLAGS_EXPERIMENTATION_OPT_OUT_MODE must be disabled, immediate, or future_experiments_only")
		}
	default:
		return mode, errortrace.Errorf("openfeature: unsupported experimentation opt-out mode: %d", mode)
	}
}

type experimentationExclusion struct {
	mode   ExperimentationOptOutMode
	cutoff time.Time
}

func (mode ExperimentationOptOutMode) exclusion(attributes map[string]any) (experimentationExclusion, error) {
	if mode != ExperimentationOptOutImmediate && mode != ExperimentationOptOutFutureExperimentsOnly {
		return experimentationExclusion{}, nil
	}
	value, present := attributes[experimentationOptOutAtAttribute]
	if !present {
		return experimentationExclusion{}, nil
	}
	stamp, ok := value.(string)
	if ok {
		if cutoff, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
			return experimentationExclusion{mode: mode, cutoff: cutoff}, nil
		}
	}
	// Reject invalid context before allocation selection; do not infer consent from it.
	// Do not include the customer-supplied value in the diagnostic.
	return experimentationExclusion{}, fmt.Errorf("%w: dd_experimentation_opt_out_at must be an RFC3339 timestamp string; supply a valid timestamp to opt out, or omit the attribute to permit experimentation", errInvalidContext)
}

func (exclusion experimentationExclusion) excludes(a *allocation) bool {
	if exclusion.mode != ExperimentationOptOutImmediate && exclusion.mode != ExperimentationOptOutFutureExperimentsOnly {
		return false
	}
	// A nil doLog logs exposures, matching evaluateFlag's metadata default.
	if a.DoLog != nil && !*a.DoLog {
		return false
	}
	switch exclusion.mode {
	case ExperimentationOptOutImmediate:
		return true
	case ExperimentationOptOutFutureExperimentsOnly:
		// Unknown activation must not qualify an allocation as older.
		return a.RandomizationActivatedAt == nil || !a.RandomizationActivatedAt.Before(exclusion.cutoff)
	default:
		return false
	}
}
