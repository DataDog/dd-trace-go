// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/telemetry"
	internalenv "github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	dynamicATRBucketCount  = 5
	dynamicATRMaxPerBucket = 20
)

// dynamicATRSettings holds the parsed dynamic ATR configuration, computed once at session setup.
type dynamicATRSettings struct {
	enabled       bool
	customBuckets *[dynamicATRBucketCount]int
}

// ciVisibilityDynamicATRSettings is the process-wide dynamic ATR configuration.
var ciVisibilityDynamicATRSettings dynamicATRSettings

// IsDynamicATREnabled reports whether duration-based ATR retry budgets are enabled.
func IsDynamicATREnabled() bool {
	return ciVisibilityDynamicATRSettings.enabled
}

// GetDynamicATRCustomBuckets returns the custom retry buckets or nil when EFD defaults should be used.
func GetDynamicATRCustomBuckets() *[dynamicATRBucketCount]int {
	return ciVisibilityDynamicATRSettings.customBuckets
}

// parseDynamicATREnabled reads DD_CIVISIBILITY_DYNAMIC_ATR_ENABLED once.
func parseDynamicATREnabled() bool {
	return internal.BoolEnv(constants.CIVisibilityDynamicATREnabledEnvironmentVariable, false)
}

// parseDynamicATRBuckets reads DD_CIVISIBILITY_DYNAMIC_ATR_BUCKETS once.
// Returns nil when unset, empty, or invalid (with a warning log), signalling
// that the EFD retry settings from the backend should be used instead.
func parseDynamicATRBuckets() *[dynamicATRBucketCount]int {
	raw, ok := internalenv.Lookup(constants.CIVisibilityDynamicATRBucketsEnvironmentVariable)
	if !ok || raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	if len(parts) != dynamicATRBucketCount {
		log.Warn("civisibility: invalid %s value %q; expected %d comma-separated integers in [1, %d]",
			constants.CIVisibilityDynamicATRBucketsEnvironmentVariable, raw, dynamicATRBucketCount, dynamicATRMaxPerBucket)
		return nil
	}

	var buckets [dynamicATRBucketCount]int
	for i, part := range parts {
		val, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			log.Warn("civisibility: invalid %s value %q; expected five comma-separated integers in [1, %d]",
				constants.CIVisibilityDynamicATRBucketsEnvironmentVariable, raw, dynamicATRMaxPerBucket)
			return nil
		}
		if val < 1 || val > dynamicATRMaxPerBucket {
			log.Warn("civisibility: invalid %s value %q; expected five comma-separated integers in [1, %d]",
				constants.CIVisibilityDynamicATRBucketsEnvironmentVariable, raw, dynamicATRMaxPerBucket)
			return nil
		}
		buckets[i] = val
	}
	return &buckets
}

// initDynamicATRSettings parses the dynamic ATR env vars once and records telemetry.
// Called from ensureAdditionalFeaturesInitialization when auto test retries is enabled.
func initDynamicATRSettings() {
	ciVisibilityDynamicATRSettings = dynamicATRSettings{
		enabled:       parseDynamicATREnabled(),
		customBuckets: parseDynamicATRBuckets(),
	}
	if ciVisibilityDynamicATRSettings.enabled {
		telemetry.DynamicATRRetriesEnabled(ciVisibilityDynamicATRSettings.customBuckets != nil)
		log.Debug("civisibility: dynamic ATR enabled [custom_buckets: %t]", ciVisibilityDynamicATRSettings.customBuckets != nil)
	}
}

// resetDynamicATRSettingsForTesting clears the global state; used only in tests.
func resetDynamicATRSettingsForTesting() {
	ciVisibilityDynamicATRSettings = dynamicATRSettings{}
}

// ResetDynamicATRSettingsForTestingExport clears the global state; used only in tests.
// Exported for use by tests in the gotesting package.
func ResetDynamicATRSettingsForTestingExport() {
	ciVisibilityDynamicATRSettings = dynamicATRSettings{}
}

// SetDynamicATRSettingsForTestingExport sets the global dynamic ATR state; used only in tests.
// Exported for use by tests in the gotesting package.
func SetDynamicATRSettingsForTestingExport(enabled bool, customBuckets *[5]int) {
	ciVisibilityDynamicATRSettings = dynamicATRSettings{
		enabled:       enabled,
		customBuckets: customBuckets,
	}
}
