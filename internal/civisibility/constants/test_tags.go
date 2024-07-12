// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package constants

const (
	// TestModule indicates the test module name.
	// This constant is used to tag traces with the name of the test module.
	TestModule = "test.module"

	// TestSuite indicates the test suite name.
	// This constant is used to tag traces with the name of the test suite.
	TestSuite = "test.suite"

	// TestName indicates the test name.
	// This constant is used to tag traces with the name of the test.
	TestName = "test.name"

	// TestType indicates the type of the test (e.g., test, benchmark).
	// This constant is used to tag traces with the type of the test.
	TestType = "test.type"

	// TestFramework indicates the test framework name.
	// This constant is used to tag traces with the name of the test framework.
	TestFramework = "test.framework"

	// TestFrameworkVersion indicates the test framework version.
	// This constant is used to tag traces with the version of the test framework.
	TestFrameworkVersion = "test.framework_version"

	// TestStatus indicates the test execution status.
	// This constant is used to tag traces with the execution status of the test.
	TestStatus = "test.status"

	// TestSkipReason indicates the skip reason of the test.
	// This constant is used to tag traces with the reason why the test was skipped.
	TestSkipReason = "test.skip_reason"

	// TestSourceFile indicates the source file where the test is located.
	// This constant is used to tag traces with the file path of the test source code.
	TestSourceFile = "test.source.file"

	// TestSourceStartLine indicates the line of the source file where the test starts.
	// This constant is used to tag traces with the line number in the source file where the test starts.
	TestSourceStartLine = "test.source.start"

	// TestCodeOwners indicates the test code owners.
	// This constant is used to tag traces with the code owners responsible for the test.
	TestCodeOwners = "test.codeowners"

	// TestCommand indicates the test command.
	// This constant is used to tag traces with the command used to execute the test.
	TestCommand = "test.command"

	// TestCommandExitCode indicates the test command exit code.
	// This constant is used to tag traces with the exit code of the test command.
	TestCommandExitCode = "test.exit_code"

	// TestCommandWorkingDirectory indicates the test command working directory relative to the source root.
	// This constant is used to tag traces with the working directory path relative to the source root.
	TestCommandWorkingDirectory = "test.working_directory"
)

// Define valid test status types.
const (
	// TestStatusPass marks test execution as passed.
	// This constant is used to tag traces with a status indicating that the test passed.
	TestStatusPass = "pass"

	// TestStatusFail marks test execution as failed.
	// This constant is used to tag traces with a status indicating that the test failed.
	TestStatusFail = "fail"

	// TestStatusSkip marks test execution as skipped.
	// This constant is used to tag traces with a status indicating that the test was skipped.
	TestStatusSkip = "skip"
)

// Define valid test types.
const (
	// TestTypeTest defines test type as test.
	// This constant is used to tag traces indicating that the type of test is a standard test.
	TestTypeTest = "test"

	// TestTypeBenchmark defines test type as benchmark.
	// This constant is used to tag traces indicating that the type of test is a benchmark.
	TestTypeBenchmark = "benchmark"
)
