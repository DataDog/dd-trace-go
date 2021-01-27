// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package ext

const (
	// TestName is a tag with specifies the test name.
	TestName = "test.name"

	// TestSuite indicates the test suite name.
	TestSuite = "test.suite"

	// TestFramework indicates the test framework name.
	TestFramework = "test.framework"

	// TestStatus indicates the test execution status.
	TestStatus = "test.status"

	// TestType indicates the type of the test (test, benchmark).
	TestType = "test.type"

	// TestSkipReason indicates the skip reason of the test.
	TestSkipReason = "test.skip_reason"
)

// Define valid test status types.
const (
	// TestStatusPass marks test execution as passed.
	TestStatusPass = "pass"

	// TestStatusFail marks test execution as failed.
	TestStatusFail = "fail"

	// TestStatusSkip marks test execution as skipped.
	TestStatusSkip = "skip"
)

// Define valid test types.
const (
	// TestTypeTest defines test type as test.
	TestTypeTest = "test"

	// TestTypeBenchmark defines test type as benchmark.
	TestTypeBenchmark = "benchmark"
)