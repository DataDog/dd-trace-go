// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package constants

const (
	// Origin is a tag used to indicate the origin of the data.
	// This tag helps in identifying the source of the trace data.
	Origin = "_dd.origin"

	// CIAppTestOrigin defines the CIApp test origin value.
	// This constant is used to tag traces that originate from CIApp test executions.
	CIAppTestOrigin = "ciapp-test"

	// TestSessionIdTagName defines the test session ID tag name for the CI Visibility Protocol.
	// This constant is used to tag traces with the ID of the test session.
	TestSessionIdTagName string = "test_session_id"

	// TestModuleIdTagName defines the test module ID tag name for the CI Visibility Protocol.
	// This constant is used to tag traces with the ID of the test module.
	TestModuleIdTagName string = "test_module_id"

	// TestSuiteIdTagName defines the test suite ID tag name for the CI Visibility Protocol.
	// This constant is used to tag traces with the ID of the test suite.
	TestSuiteIdTagName string = "test_suite_id"

	// ItrCorrelationIdTagName defines the correlation ID for the intelligent test runner tag name for the CI Visibility Protocol.
	// This constant is used to tag traces with the correlation ID for intelligent test runs.
	ItrCorrelationIdTagName string = "itr_correlation_id"
)

// Coverage tags
const (
	// CodeCoverageEnabledTagName defines if code coverage has been enabled.
	// This constant is used to tag traces to indicate whether code coverage measurement is enabled.
	CodeCoverageEnabledTagName string = "test.code_coverage.enabled"

	// CodeCoveragePercentageOfTotalLines defines the percentage of total code coverage by a session.
	// This constant is used to tag traces with the percentage of code lines covered during the test session.
	CodeCoveragePercentageOfTotalLines string = "test.code_coverage.lines_pct"
)
