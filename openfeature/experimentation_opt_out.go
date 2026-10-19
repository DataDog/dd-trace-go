// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"fmt"
	"time"
)

// ExperimentationOptOutMode controls per-subject exclusion from experimentation.
type ExperimentationOptOutMode int

const (
	// ExperimentationOptOutDisabled ignores dd_experimentation_opt_out_at.
	// This is the default mode.
	ExperimentationOptOutDisabled ExperimentationOptOutMode = iota
	// ExperimentationOptOutImmediate skips exposure-logging allocations when
	// dd_experimentation_opt_out_at contains an RFC3339 timestamp string.
	// It applies on each evaluation, including to experiments already in progress.
	// The timestamp records the customer's preference; it does not schedule exclusion.
	ExperimentationOptOutImmediate
)

const experimentationOptOutAtAttribute = "dd_experimentation_opt_out_at"

func (mode ExperimentationOptOutMode) excludeExperiments(attributes map[string]any) (bool, error) {
	if mode == ExperimentationOptOutDisabled {
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
	// Do not include the customer-supplied value in an error or treat it as opt-in.
	return false, fmt.Errorf("%w: dd_experimentation_opt_out_at must be an RFC3339 timestamp string", errInvalidContext)
}
