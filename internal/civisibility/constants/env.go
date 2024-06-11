// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package constants

const (
	// CiVisibilityEnabledEnvironmnetVariable indicates if CI Visibility mode is enabled.
	// This environment variable should be set to "1" or "true" to enable CI Visibility mode, which activates tracing and other
	// features related to CI Visibility in the Datadog platform.
	CiVisibilityEnabledEnvironmnetVariable = "DD_CIVISIBILITY_ENABLED"

	// CiVisibilityAgentlessEnabledEnvironmentVariable indicates if CI Visibility agentless mode is enabled.
	// This environment variable should be set to "1" or "true" to enable agentless mode for CI Visibility, where traces
	// are sent directly to Datadog without using a local agent.
	CiVisibilityAgentlessEnabledEnvironmentVariable = "DD_CIVISIBILITY_AGENTLESS_ENABLED"

	// CiVisibilityAgentlessUrlEnvironmentVariable forces the agentless URL to a custom one.
	// This environment variable allows you to specify a custom URL for the agentless intake in CI Visibility mode.
	CiVisibilityAgentlessUrlEnvironmentVariable = "DD_CIVISIBILITY_AGENTLESS_URL"

	// ApiKeyEnvironmentVariable indicates the API key to be used for agentless intake.
	// This environment variable should be set to your Datadog API key, allowing the agentless mode to authenticate and
	// send data directly to the Datadog platform.
	ApiKeyEnvironmentVariable = "DD_API_KEY"
)
