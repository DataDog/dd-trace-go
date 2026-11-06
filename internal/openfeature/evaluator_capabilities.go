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

// DependentFlagsVariantKeyQueryParameter requests prerequisite variant keys in UFC responses.
const DependentFlagsVariantKeyQueryParameter = "cap_dependent-flags-variant-key"

// DependentFlagsVariantKeyQueryVersion is the supported query parameter version.
const DependentFlagsVariantKeyQueryVersion = "v1"

// EvaluatorCapabilities reports features supported by the Go evaluator and exposure hooks.
// The tracer needs this report before the provider exists, so it lives in the
// lightweight internal package.
func EvaluatorCapabilities() []string { return []string{DependentFlagVariantKeyV1} }

func remoteConfigCapabilities(evaluatorCapabilities []string) []remoteconfig.Capability {
	capabilities := []remoteconfig.Capability{remoteconfig.FFEFlagEvaluation}
	if slices.Contains(evaluatorCapabilities, DependentFlagVariantKeyV1) {
		capabilities = append(capabilities, remoteconfig.FFEFlagDependentFlagVariantKeyV1)
	}
	return capabilities
}
