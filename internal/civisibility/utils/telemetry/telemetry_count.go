// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package telemetry

import "gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

// EventCreated the number of events created by CI Visibility
func EventCreated(testingFramework TestingFramework, eventType TestingEventType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "event_created", 1.0, removeEmptyStrings([]string{
		(string)(testingFramework),
		(string)(eventType),
	}), true)
}

// EventFinished the number of events finished by CI Visibility
func EventFinished(testingFramework TestingFramework, eventType TestingEventType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "event_finished", 1.0, removeEmptyStrings([]string{
		(string)(testingFramework),
		(string)(eventType),
	}), true)
}

// CodeCoverageStarted the number of code coverage start calls by CI Visibility
func CodeCoverageStarted(testingFramework TestingFramework, coverageLibraryType CoverageLibraryType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "code_coverage_started", 1.0, removeEmptyStrings([]string{
		(string)(testingFramework),
		(string)(coverageLibraryType),
	}), true)
}

// CodeCoverageFinished the number of code coverage finished calls by CI Visibility
func CodeCoverageFinished(testingFramework TestingFramework, coverageLibraryType CoverageLibraryType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "code_coverage_finished", 1.0, removeEmptyStrings([]string{
		(string)(testingFramework),
		(string)(coverageLibraryType),
	}), true)
}

// EventsEnqueueForSerialization the number of events enqueued for serialization by CI Visibility
func EventsEnqueueForSerialization() {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "events_enqueued_for_serialization", 1.0, nil, true)
}

// EndpointPayloadRequests the number of requests sent to the endpoint, regardless of success, tagged by endpoint type
func EndpointPayloadRequests(endpointAndCompressionType EndpointAndCompressionType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "endpoint_payload.requests", 1.0, removeEmptyStrings([]string{
		(string)(endpointAndCompressionType),
	}), true)
}

// EndpointPayloadRequestsErrors the number of requests sent to the endpoint that errored, tagget by the error type and endpoint type and status code
func EndpointPayloadRequestsErrors(endpointType EndpointType, errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "endpoint_payload.requests_errors", 1.0, removeEmptyStrings([]string{
		(string)(endpointType),
		(string)(errorType),
	}), true)
}

// EndpointPayloadDropped the number of payloads dropped after all retries by CI Visibility
func EndpointPayloadDropped(endpointType EndpointType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "endpoint_payload.dropped", 1.0, removeEmptyStrings([]string{
		(string)(endpointType),
	}), true)
}

// GitCommand the number of git commands executed by CI Visibility
func GitCommand(commandType CommandType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git.command", 1.0, removeEmptyStrings([]string{
		(string)(commandType),
	}), true)
}

// GitCommandErrors the number of git command that errored by CI Visibility
func GitCommandErrors(commandType CommandType, exitCode CommandExitCodeType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git.command_errors", 1.0, removeEmptyStrings([]string{
		(string)(commandType),
		(string)(exitCode),
	}), true)
}

// GitRequestsSearchCommits the number of requests sent to the search commit endpoint, regardless of success.
func GitRequestsSearchCommits(requestCompressed RequestCompressedType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.search_commits", 1.0, removeEmptyStrings([]string{
		(string)(requestCompressed),
	}), true)
}

// GitRequestsSearchCommitsErrors the number of requests sent to the search commit endpoint that errored, tagged by the error type.
func GitRequestsSearchCommitsErrors(errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.search_commits_errors", 1.0, removeEmptyStrings([]string{
		(string)(errorType),
	}), true)
}

// GitRequestsObjectsPack the number of requests sent to the objects pack endpoint, tagged by the request compressed type.
func GitRequestsObjectsPack(requestCompressed RequestCompressedType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.objects_pack", 1.0, removeEmptyStrings([]string{
		(string)(requestCompressed),
	}), true)
}

// GitRequestsObjectsPackErrors the number of requests sent to the objects pack endpoint that errored, tagged by the error type.
func GitRequestsObjectsPackErrors(errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.objects_pack_errors", 1.0, removeEmptyStrings([]string{
		(string)(errorType),
	}), true)
}

// GitRequestsSettings the number of requests sent to the settings endpoint, tagged by the request compressed type.
func GitRequestsSettings(requestCompressed RequestCompressedType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.settings", 1.0, removeEmptyStrings([]string{
		(string)(requestCompressed),
	}), true)
}

// GitRequestsSettingsErrors the number of requests sent to the settings endpoint that errored, tagged by the error type.
func GitRequestsSettingsErrors(errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.settings_errors", 1.0, removeEmptyStrings([]string{
		(string)(errorType),
	}), true)
}

// GitRequestsSettingsResponse the number of settings responses received by CI Visibility, tagged by the settings response type.
func GitRequestsSettingsResponse(settingsResponseType SettingsResponseType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "git_requests.settings_response", 1.0, removeEmptyStrings([]string{
		(string)(settingsResponseType),
	}), true)
}

// ITRSkippableTestsRequest the number of requests sent to the ITR skippable tests endpoint, tagged by the request compressed type.
func ITRSkippableTestsRequest(requestCompressed RequestCompressedType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_skippable_tests.request", 1.0, removeEmptyStrings([]string{
		(string)(requestCompressed),
	}), true)
}

// ITRSkippableTestsRequestErrors the number of requests sent to the ITR skippable tests endpoint that errored, tagged by the error type.
func ITRSkippableTestsRequestErrors(errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_skippable_tests.request_errors", 1.0, removeEmptyStrings([]string{
		(string)(errorType),
	}), true)
}

// ITRSkippableTestsResponseTests the number of tests received in the ITR skippable tests response by CI Visibility.
func ITRSkippableTestsResponseTests() {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_skippable_tests.response_tests", 1.0, nil, true)
}

// ITRSkippableTestsResponseSuites the number of suites received in the ITR skippable tests response by CI Visibility.
func ITRSkippableTestsResponseSuites() {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_skippable_tests.response_suites", 1.0, nil, true)
}

// ITRSkipped the number of ITR tests skipped by CI Visibility, tagged by the event type.
func ITRSkipped(eventType TestingEventType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_skipped", 1.0, removeEmptyStrings([]string{
		(string)(eventType),
	}), true)
}

// ITRUnskippable the number of ITR tests unskippable by CI Visibility, tagged by the event type.
func ITRUnskippable(eventType TestingEventType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_unskippable", 1.0, removeEmptyStrings([]string{
		(string)(eventType),
	}), true)
}

// ITRForcedRun the number of tests or test suites that would've been skipped by ITR but were forced to run because of their unskippable status by CI Visibility.
func ITRForcedRun(eventType TestingEventType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "itr_forced_run", 1.0, removeEmptyStrings([]string{
		(string)(eventType),
	}), true)
}

// CodeCoverageIsEmpty the number of code coverage payloads that are empty by CI Visibility.
func CodeCoverageIsEmpty() {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "code_coverage.is_empty", 1.0, nil, true)
}

// CodeCoverageErrors the number of errors while processing code coverage by CI Visibility.
func CodeCoverageErrors() {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "code_coverage.errors", 1.0, nil, true)
}

// EarlyFlakeDetectionRequest the number of requests sent to the early flake detection endpoint, tagged by the request compressed type.
func EarlyFlakeDetectionRequest(requestCompressed RequestCompressedType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "early_flake_detection.request", 1.0, removeEmptyStrings([]string{
		(string)(requestCompressed),
	}), true)
}

// EarlyFlakeDetectionRequestErrors the number of requests sent to the early flake detection endpoint that errored, tagged by the error type.
func EarlyFlakeDetectionRequestErrors(errorType ErrorType) {
	telemetry.GlobalClient.Count(telemetry.NamespaceCiVisibility, "early_flake_detection.request_errors", 1.0, removeEmptyStrings([]string{
		(string)(errorType),
	}), true)
}