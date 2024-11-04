// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package utils

import "gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

// TestingFramework is a type for testing frameworks
type TestingFramework string

const (
	GoTestingFramework TestingFramework = "test_framework:testing"
	UnknownFramework   TestingFramework = "test_framework:unknown"
)

// TestingEventType is a type for testing event types
type TestingEventType string

const (
	TestEventType                                                   TestingEventType = "event_type:test"
	BenchmarkTestEventType                                          TestingEventType = "event_type:test;is_benchmark"
	SuiteEventType                                                  TestingEventType = "event_type:suite"
	ModuleEventType                                                 TestingEventType = "event_type:module"
	SessionNoCodeOwnerIsSupportedCiEventType                        TestingEventType = "event_type:session"
	SessionNoCodeOwnerUnsupportedCiEventType                        TestingEventType = "event_type:session;is_unsupported_ci"
	SessionHasCodeOwnerIsSupportedCiEventType                       TestingEventType = "event_type:session;has_codeowner"
	SessionHasCodeOwnerUnsupportedCiEventType                       TestingEventType = "event_type:session;has_codeowner;is_unsupported_ci"
	TestEfdTestIsNewEventType                                       TestingEventType = "event_type:test;is_new:true"
	TestEfdTestIsNewEfdAbortSlowEventType                           TestingEventType = "event_type:test;is_new:true;early_flake_detection_abort_reason:slow"
	TestBrowserDriverSeleniumEventType                              TestingEventType = "event_type:test;browser_driver:selenium"
	TestEfdTestIsNewBrowserDriverSeleniumEventType                  TestingEventType = "event_type:test;is_new:true;browser_driver:selenium"
	TestEfdTestIsNewEfdAbortSlowBrowserDriverSeleniumEventType      TestingEventType = "event_type:test;is_new:true;early_flake_detection_abort_reason:slow;browser_driver:selenium"
	TestBrowserDriverSeleniumIsRumEventType                         TestingEventType = "event_type:test;browser_driver:selenium;is_rum:true"
	TestEfdTestIsNewBrowserDriverSeleniumIsRumEventType             TestingEventType = "event_type:test;is_new:true;browser_driver:selenium;is_rum:true"
	TestEfdTestIsNewEfdAbortSlowBrowserDriverSeleniumIsRumEventType TestingEventType = "event_type:test;is_new:true;early_flake_detection_abort_reason:slow;browser_driver:selenium;is_rum:true"
)

// CoverageLibraryType is a type for coverage library types
type CoverageLibraryType string

const (
	DefaultCoverageLibraryType CoverageLibraryType = "library:default"
	UnknownCoverageLibraryType CoverageLibraryType = "library:unknown"
)

// EndpointAndCompressionType is a type for endpoint and compression types
type EndpointAndCompressionType string

const (
	TestCycleUncompressedEndpointType         EndpointAndCompressionType = "endpoint:test_cycle"
	TestCycleRequestCompressedEndpointType    EndpointAndCompressionType = "endpoint:test_cycle;rq_compressed:true"
	CodeCoverageUncompressedEndpointType      EndpointAndCompressionType = "endpoint:code_coverage"
	CodeCoverageRequestCompressedEndpointType EndpointAndCompressionType = "endpoint:code_coverage;rq_compressed:true"
)

// EndpointType is a type for endpoint types
type EndpointType string

const (
	TestCycleEndpointType    EndpointType = "endpoint:test_cycle"
	CodeCoverageEndpointType EndpointType = "endpoint:code_coverage"
)

// ErrorType is a type for error types
type ErrorType string

const (
	TimeoutErrorType       ErrorType = "error_type:timeout"
	NetworkErrorType       ErrorType = "error_type:network"
	StatusCodeErrorType    ErrorType = "error_type:status_code"
	StatusCode4xxErrorType ErrorType = "error_type:status_code_4xx_response"
	StatusCode5xxErrorType ErrorType = "error_type:status_code_5xx_response"
	StatusCode400ErrorType ErrorType = "error_type:status_code_4xx_response;status_code:400"
	StatusCode401ErrorType ErrorType = "error_type:status_code_4xx_response;status_code:401"
	StatusCode403ErrorType ErrorType = "error_type:status_code_4xx_response;status_code:403"
	StatusCode404ErrorType ErrorType = "error_type:status_code_4xx_response;status_code:404"
	StatusCode408ErrorType ErrorType = "error_type:status_code_4xx_response;status_code:408"
	StatusCode429ErrorType ErrorType = "error_type:status_code_4xx_response;status_code:429"
)

// CommandType is a type for commands types
type CommandType string

const (
	GetRepositoryCommandsType   CommandType = "command:get_repository"
	GetBranchCommandsType       CommandType = "command:get_branch"
	GetRemoteCommandsType       CommandType = "command:get_remote"
	GetHeadCommandsType         CommandType = "command:get_head"
	CheckShallowCommandsType    CommandType = "command:check_shallow"
	UnshallowCommandsType       CommandType = "command:unshallow"
	GetLocalCommitsCommandsType CommandType = "command:get_local_commits"
	GetObjectsCommandsType      CommandType = "command:get_objects"
	PackObjectsCommandsType     CommandType = "command:pack_objects"
)

// CommandExitCodeType is a type for command exit codes
type CommandExitCodeType string

const (
	MissingCommandExitCode  CommandExitCodeType = "exit_code:missing"
	UnknownCommandExitCode  CommandExitCodeType = "exit_code:unknown"
	ECMinus1CommandExitCode CommandExitCodeType = "exit_code:-1"
	EC1CommandExitCode      CommandExitCodeType = "exit_code:1"
	EC2CommandExitCode      CommandExitCodeType = "exit_code:2"
	EC127CommandExitCode    CommandExitCodeType = "exit_code:127"
	EC128CommandExitCode    CommandExitCodeType = "exit_code:128"
	EC129CommandExitCode    CommandExitCodeType = "exit_code:129"
)

// RequestCompressedType is a type for request compressed types
type RequestCompressedType string

const (
	UncompressedRequestCompressedType RequestCompressedType = ""
	CompressedRequestCompressedType   RequestCompressedType = "rq_compressed:true"
)

// ResponseCompressedType is a type for response compressed types
type ResponseCompressedType string

const (
	UncompressedResponseCompressedType ResponseCompressedType = ""
	CompressedResponseCompressedType   ResponseCompressedType = "rs_compressed:true"
)

// SettingsResponseType is a type for settings response types
type SettingsResponseType string

const (
	CoverageDisabledItrSkipDisabledSettingsResponseType           SettingsResponseType = ""
	CoverageEnabledItrSkipDisabledSettingsResponseType            SettingsResponseType = "coverage_enabled"
	CoverageDisabledItrSkipEnabledSettingsResponseType            SettingsResponseType = "itrskip_enabled"
	CoverageEnabledItrSkipEnabledSettingsResponseType             SettingsResponseType = "coverage_enabled;itrskip_enabled"
	CoverageDisabledItrSkipDisabledEfdEnabledSettingsResponseType SettingsResponseType = "early_flake_detection_enabled:true"
	CoverageEnabledItrSkipDisabledEfdEnabledSettingsResponseType  SettingsResponseType = "coverage_enabled;early_flake_detection_enabled:true"
	CoverageDisabledItrSkipEnabledEfdEnabledSettingsResponseType  SettingsResponseType = "itrskip_enabled;early_flake_detection_enabled:true"
	CoverageEnabledItrSkipEnabledEfdEnabledSettingsResponseType   SettingsResponseType = "coverage_enabled;itrskip_enabled;early_flake_detection_enabled:true"
)

// EventCreated the number of events created by CI Visibility
func EventCreated(testingFramework TestingFramework, eventType TestingEventType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "event_created", 1.0, []string{
		(string)(testingFramework),
		(string)(eventType),
	}, true)
}

// EventFinished the number of events finished by CI Visibility
func EventFinished(testingFramework TestingFramework, eventType TestingEventType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "event_finished", 1.0, []string{
		(string)(testingFramework),
		(string)(eventType),
	}, true)
}

// CodeCoverageStarted the number of code coverage start calls by CI Visibility
func CodeCoverageStarted(testingFramework TestingFramework, coverageLibraryType CoverageLibraryType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "code_coverage_started", 1.0, []string{
		(string)(testingFramework),
		(string)(coverageLibraryType),
	}, true)
}

// CodeCoverageFinished the number of code coverage finished calls by CI Visibility
func CodeCoverageFinished(testingFramework TestingFramework, coverageLibraryType CoverageLibraryType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "code_coverage_finished", 1.0, []string{
		(string)(testingFramework),
		(string)(coverageLibraryType),
	}, true)
}

// EventsEnqueueForSerialization the number of events enqueued for serialization by CI Visibility
func EventsEnqueueForSerialization() {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "events_enqueued_for_serialization", 1.0, []string{}, true)
}

// EndpointPayloadRequests the number of requests sent to the endpoint, regardless of success, tagged by endpoint type
func EndpointPayloadRequests(endpointAndCompressionType EndpointAndCompressionType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "endpoint_payload.requests", 1.0, []string{
		(string)(endpointAndCompressionType),
	}, true)
}

// EndpointPayloadRequestsErrors the number of requests sent to the endpoint that errored, tagget by the error type and endpoint type and status code
func EndpointPayloadRequestsErrors(endpointType EndpointType, errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "endpoint_payload.requests_errors", 1.0, []string{
		(string)(endpointType),
		(string)(errorType),
	}, true)
}

// EndpointPayloadDropped the number of payloads dropped after all retries by CI Visibility
func EndpointPayloadDropped(endpointType EndpointType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "endpoint_payload.dropped", 1.0, []string{
		(string)(endpointType),
	}, true)
}

// GitCommand the number of git commands executed by CI Visibility
func GitCommand(commandType CommandType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git.command", 1.0, []string{
		(string)(commandType),
	}, true)
}

// GitCommandErrors the number of git command that errored by CI Visibility
func GitCommandErrors(commandType CommandType, exitCode CommandExitCodeType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git.command_errors", 1.0, []string{
		(string)(commandType),
		(string)(exitCode),
	}, true)
}

// GitRequestsSearchCommits the number of requests sent to the search commit endpoint, regardless of success.
func GitRequestsSearchCommits(requestCompressed RequestCompressedType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.search_commits", 1.0, []string{
		(string)(requestCompressed),
	}, true)
}

// GitRequestsSearchCommitsErrors the number of requests sent to the search commit endpoint that errored, tagged by the error type.
func GitRequestsSearchCommitsErrors(errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.search_commits_errors", 1.0, []string{
		(string)(errorType),
	}, true)
}

// GitRequestsObjectsPack the number of requests sent to the objects pack endpoint, tagged by the request compressed type.
func GitRequestsObjectsPack(requestCompressed RequestCompressedType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.objects_pack", 1.0, []string{
		(string)(requestCompressed),
	}, true)
}

// GitRequestsObjectsPackErrors the number of requests sent to the objects pack endpoint that errored, tagged by the error type.
func GitRequestsObjectsPackErrors(errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.objects_pack_errors", 1.0, []string{
		(string)(errorType),
	}, true)
}

// GitRequestsSettings the number of requests sent to the settings endpoint, tagged by the request compressed type.
func GitRequestsSettings(requestCompressed RequestCompressedType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.settings", 1.0, []string{
		(string)(requestCompressed),
	}, true)
}

// GitRequestsSettingsErrors the number of requests sent to the settings endpoint that errored, tagged by the error type.
func GitRequestsSettingsErrors(errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.settings_errors", 1.0, []string{
		(string)(errorType),
	}, true)
}

// GitRequestsSettingsResponse the number of settings responses received by CI Visibility, tagged by the settings response type.
func GitRequestsSettingsResponse(settingsResponseType SettingsResponseType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.settings_response", 1.0, []string{
		(string)(settingsResponseType),
	}, true)
}

// ITRSkippableTestsRequest the number of requests sent to the ITR skippable tests endpoint, tagged by the request compressed type.
func ITRSkippableTestsRequest(requestCompressed RequestCompressedType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_skippable_tests.request", 1.0, []string{
		(string)(requestCompressed),
	}, true)
}

// ITRSkippableTestsRequestErrors the number of requests sent to the ITR skippable tests endpoint that errored, tagged by the error type.
func ITRSkippableTestsRequestErrors(errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_skippable_tests.request_errors", 1.0, []string{
		(string)(errorType),
	}, true)
}

// ITRSkippableTestsResponseTests the number of tests received in the ITR skippable tests response by CI Visibility.
func ITRSkippableTestsResponseTests() {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_skippable_tests.response_tests", 1.0, []string{}, true)
}

// ITRSkippableTestsResponseSuites the number of suites received in the ITR skippable tests response by CI Visibility.
func ITRSkippableTestsResponseSuites() {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_skippable_tests.response_suites", 1.0, []string{}, true)
}

// ITRSkipped the number of ITR tests skipped by CI Visibility, tagged by the event type.
func ITRSkipped(eventType TestingEventType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_skipped", 1.0, []string{
		(string)(eventType),
	}, true)
}

// ITRUnskippable the number of ITR tests unskippable by CI Visibility, tagged by the event type.
func ITRUnskippable(eventType TestingEventType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_unskippable", 1.0, []string{
		(string)(eventType),
	}, true)
}

// ITRForcedRun the number of tests or test suites that would've been skipped by ITR but were forced to run because of their unskippable status by CI Visibility.
func ITRForcedRun(eventType TestingEventType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_forced_run", 1.0, []string{
		(string)(eventType),
	}, true)
}

// CodeCoverageIsEmpty the number of code coverage payloads that are empty by CI Visibility.
func CodeCoverageIsEmpty() {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "code_coverage.is_empty", 1.0, []string{}, true)
}

// CodeCoverageErrors the number of errors while processing code coverage by CI Visibility.
func CodeCoverageErrors() {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "code_coverage.errors", 1.0, []string{}, true)
}

// EarlyFlakeDetectionRequest the number of requests sent to the early flake detection endpoint, tagged by the request compressed type.
func EarlyFlakeDetectionRequest(requestCompressed RequestCompressedType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "early_flake_detection.request", 1.0, []string{
		(string)(requestCompressed),
	}, true)
}

// EarlyFlakeDetectionRequestErrors the number of requests sent to the early flake detection endpoint that errored, tagged by the error type.
func EarlyFlakeDetectionRequestErrors(errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "early_flake_detection.request_errors", 1.0, []string{
		(string)(errorType),
	}, true)
}
