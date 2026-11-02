// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"slices"

	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

// DependentFlagVariantKeyV1 identifies prerequisite variant-key evaluation support.
const DependentFlagVariantKeyV1 = "dependent-flag-variant-key-v1"

// EvaluatorCapabilities reports features supported by the Go evaluator and exposure hooks.
// Keep this empty until prerequisite evaluation, error propagation, and exposure
// reporting are implemented and integration-tested. The tracer needs this report
// before the provider exists, so it lives in the lightweight internal package.
func EvaluatorCapabilities() []string { return nil }

func remoteConfigCapabilities(evaluatorCapabilities []string) []remoteconfig.Capability {
	capabilities := []remoteconfig.Capability{remoteconfig.FFEFlagEvaluation}
	if slices.Contains(evaluatorCapabilities, DependentFlagVariantKeyV1) {
		capabilities = append(capabilities, remoteconfig.FFEFlagDependentFlagVariantKeyV1)
	}
	return capabilities
}
