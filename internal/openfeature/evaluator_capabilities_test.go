// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

func TestEvaluatorCapabilities(t *testing.T) {
	assert.Empty(t, EvaluatorCapabilities())
	assert.Equal(t, remoteconfig.Capability(50), remoteconfig.FFEFlagDependentFlagVariantKeyV1)
	for _, test := range []struct {
		name         string
		capabilities []string
		expected     []remoteconfig.Capability
	}{
		{"absent", nil, []remoteconfig.Capability{46}},
		{"empty", []string{}, []remoteconfig.Capability{46}},
		{"unknown", []string{"unknown-v1"}, []remoteconfig.Capability{46}},
		{"supported", []string{DependentFlagVariantKeyV1}, []remoteconfig.Capability{46, 50}},
		{"mixed", []string{"unknown-v1", DependentFlagVariantKeyV1}, []remoteconfig.Capability{46, 50}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, remoteConfigCapabilities(test.capabilities))
		})
	}
}
