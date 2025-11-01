// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

// func TestConfigHasFields(t *testing.T) {
// 	// TODO: Use supported configurations JSON as expectedFields instead
// 	expectedFields := map[string]reflect.Type{
// 		"AgentURL":         reflect.TypeOf((*url.URL)(nil)),
// 		"Debug":            reflect.TypeOf(false),
// 		"LogToStdout":      reflect.TypeOf(false),
// 		"LogStartup":       reflect.TypeOf(false),
// 		"ServiceName":      reflect.TypeOf(""),
// 		"Version":          reflect.TypeOf(""),
// 		"Env":              reflect.TypeOf(""),
// 		//"Sampler": reflect.TypeOf((*RateSampler)(nil)),
// 		// "OriginalAgentURL": reflect.TypeOf((*url.URL)(nil)), // We probably don't need this anymore
// 		"ServiceMappings": reflect.TypeOf((map[string]string)(nil)),
// 		// "GlobalTags": reflect.TypeOf((*dynamicConfig[map[string]interface{}])(nil)),
// 		// "Transport": reflect.TypeOf((*transport)(nil)),
// 		"HTTPClientTimeout": reflect.TypeOf(int64(0)),
// 		// "Propagator": reflect.TypeOf((*Propagator)(nil)),
// 		"Hostname": reflect.TypeOf(""),
// 		// "Logger": reflect.TypeOf((*Logger)(nil)),
// 		"RuntimeMetrics":   reflect.TypeOf(false),
// 		"RuntimeMetricsV2": reflect.TypeOf(false),
// 		// "StatsdClient": reflect.TypeOf((*internal.StatsdClient)(nil)),
// 		// "SpanRules": reflect.TypeOf((*[]SamplingRule)(nil)),
// 		// "TraceRules": reflect.TypeOf((*[]SamplingRule)(nil)),
// 		"ProfilerHotspots":  reflect.TypeOf(false),
// 		"ProfilerEndpoints": reflect.TypeOf(false),
// 		// "TracingEnabled":               reflect.TypeOf((*dynamicConfig[bool])(nil)),
// 		"EnableHostnameDetection":      reflect.TypeOf(false),
// 		"SpanAttributeSchemaVersion":   reflect.TypeOf(0),
// 		"PeerServiceDefaultsEnabled":   reflect.TypeOf(false),
// 		"PeerServiceMappings":          reflect.TypeOf((map[string]string)(nil)),
// 		"DebugAbandonedSpans":          reflect.TypeOf(false),
// 		"SpanTimeout":                  reflect.TypeOf(time.Duration(int64(0))),
// 		"PartialFlushMinSpans":         reflect.TypeOf(0),
// 		"PartialFlushEnabled":          reflect.TypeOf(false),
// 		"StatsComputationEnabled":      reflect.TypeOf(false),
// 		"DataStreamsMonitoringEnabled": reflect.TypeOf(false),
// 		// "OrchestrionCfg": reflect.TypeOf((*orchestrionConfig)(nil)),
// 		// "TraceSampleRate": reflect.TypeOf((*dynamicConfig[float64])(nil)),
// 		// "TraceSampleRules": reflect.TypeOf((*dynamicConfig[[]SamplingRule])(nil)),
// 		// "HeaderAsTags": reflect.TypeOf((*dynamicConfig[[]string])(nil)),
// 		"DynamicInstrumentationEnabled": reflect.TypeOf(false),
// 		"GlobalSampleRate":              reflect.TypeOf(float64(0)),
// 		"CIVisibilityEnabled":           reflect.TypeOf(false),
// 		"CIVisibilityAgentless":         reflect.TypeOf(false),
// 		"LogDirectory":                  reflect.TypeOf(""),
// 		"TracingAsTransport":            reflect.TypeOf(false),
// 		"TraceRateLimitPerSecond":       reflect.TypeOf(float64(0)),
// 		"TraceProtocol":                 reflect.TypeOf(float64(0)),
// 		// "LLMObsEnabled": reflect.TypeOf(false),
// 		// "LLMObsMLApp": reflect.TypeOf(""),
// 		// "LLMObsAgentlessEnabled": reflect.TypeOf(false),
// 		// "LLMObsProjectName": reflect.TypeOf(""),
// 	}

// 	// Get the Config struct type
// 	configType := reflect.TypeOf(Config{})

// 	// Verify the number of expected fields matches the actual number of fields
// 	actualFieldCount := configType.NumField()
// 	expectedFieldCount := len(expectedFields)
// 	assert.Equal(t, expectedFieldCount, actualFieldCount,
// 		"Expected %d fields in Config struct, but found %d. Update the test when adding/removing fields.",
// 		expectedFieldCount, actualFieldCount)

// 	// Verify each expected field exists with the correct type
// 	for fieldName, expectedType := range expectedFields {
// 		field, found := configType.FieldByName(fieldName)
// 		assert.True(t, found, "Field %s should exist on Config struct", fieldName)

// 		if found {
// 			assert.Equal(t, expectedType, field.Type,
// 				"Field %s should have type %s, but has type %s",
// 				fieldName, expectedType, field.Type)
// 		}
// 	}

// 	// Verify we can instantiate the config
// 	cfg := new(Config)
// 	assert.NotNil(t, cfg, "Should be able to create new Config instance")
// }
