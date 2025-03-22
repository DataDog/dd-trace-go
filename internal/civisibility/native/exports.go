// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build civisibility_native
// +build civisibility_native

package main

// #cgo darwin CFLAGS: -mmacosx-version-min=11.0
// #cgo darwin LDFLAGS: -s -w
// #cgo windows CFLAGS: -Os
// #cgo windows LDFLAGS: -Os
// #include <stdlib.h>
/*
// Bool is a typedef for unsigned char used as a boolean type.
// Values: 1 for true, 0 for false.
typedef unsigned char Bool;

// Uint64 is a typedef for unsigned long long, representing a 64-bit unsigned integer.
typedef unsigned long long Uint64;

// topt_TslvId is a typedef representing a generic 64-bit identifier used by sessions, modules, suites, and tests.
typedef Uint64 topt_TslvId;

// topt_SessionId is the identifier type for test sessions, uniquely representing a single test session in the system.
typedef topt_TslvId topt_SessionId;

// topt_ModuleId is the identifier type for test modules, uniquely representing a single module within a test session.
typedef topt_TslvId topt_ModuleId;

// topt_SuiteId is the identifier type for test suites, uniquely representing a test suite within a module.
typedef topt_TslvId topt_SuiteId;

// topt_TestId is the identifier type for individual tests, uniquely representing a test within a suite.
typedef topt_TslvId topt_TestId;

// topt_TestStatus is used to represent the outcome/status of a test.
// Possible values:
//   - topt_TestStatusPass (0): The test passed successfully.
//   - topt_TestStatusFail (1): The test failed.
//   - topt_TestStatusSkip (2): The test was skipped.
typedef unsigned char topt_TestStatus;

// topt_TestStatusPass is the status for a passing test.
static const topt_TestStatus topt_TestStatusPass = 0;
// topt_TestStatusFail is the status for a failing test.
static const topt_TestStatus topt_TestStatusFail = 1;
// topt_TestStatusSkip is the status for a skipped test.
static const topt_TestStatus topt_TestStatusSkip = 2;

// topt_SessionResult is returned by operations that create or retrieve a test session.
// Fields:
//   - session_id: The ID of the created or found session.
//   - valid: A Bool indicating whether the operation succeeded.
typedef struct {
	topt_SessionId session_id;
	Bool valid;
} topt_SessionResult;

// topt_ModuleResult is returned by operations that create or retrieve a test module.
// Fields:
//   - module_id: The ID of the created or found module.
//   - valid: A Bool indicating whether the operation succeeded.
typedef struct {
	topt_ModuleId module_id;
	Bool valid;
} topt_ModuleResult;

// topt_SuiteResult is returned by operations that create or retrieve a test suite.
// Fields:
//   - suite_id: The ID of the created or found suite.
//   - valid: A Bool indicating whether the operation succeeded.
typedef struct {
	topt_SuiteId suite_id;
	Bool valid;
} topt_SuiteResult;

// topt_TestResult is returned by operations that create a test.
// Fields:
//   - test_id: The ID of the created test.
//   - valid: A Bool indicating whether the operation succeeded.
typedef struct {
	topt_TestId test_id;
	Bool valid;
} topt_TestResult;

// topt_KeyValuePair represents a single string-based key-value pair, commonly used for tags and metadata.
// Fields:
//   - key: A C-string containing the key.
//   - value: A C-string containing the value.
typedef struct {
    char* key;
    char* value;
} topt_KeyValuePair;

// topt_KeyValueArray represents an array of key-value pairs.
// Fields:
//   - data: Pointer to an array of topt_KeyValuePair.
//   - len: The length of the array.
//
// Used for passing lists of environment variables, tags, and other string metadata.
typedef struct {
    topt_KeyValuePair* data;
    size_t len;
} topt_KeyValueArray;

// topt_KeyNumberPair represents a single numeric key-value pair for numerical tags or metrics.
// Fields:
//   - key: A C-string containing the key.
//   - value: A double representing the numeric value.
typedef struct {
	char* key;
	double value;
} topt_KeyNumberPair;

// topt_KeyNumberArray represents an array of numeric key-value pairs.
// Fields:
//   - data: Pointer to an array of topt_KeyNumberPair.
//   - len: The length of the array.
//
// Used for passing lists of numeric metrics, measurements, or counters as tags.
typedef struct {
	topt_KeyNumberPair* data;
	size_t len;
} topt_KeyNumberArray;

// topt_InitOptions holds initialization data for the library.
// Fields:
//   - language: The programming language of the runtime (e.g., "go", "python"), optional.
//   - runtime_name: The runtime name (e.g., "go", "dotnet"), optional.
//   - runtime_version: The runtime version string (e.g., "1.18", "6.0"), optional.
//   - working_directory: The directory where tests are executed, optional.
//   - environment_variables: A pointer to a topt_KeyValueArray of environment variables, optional.
//   - global_tags: A pointer to a topt_KeyValueArray of global tags, optional.
//   - use_mock_tracer: A Bool indicating whether to use a mock tracer for testing.
//   - unused01 ... unused05: Reserved fields for future use.
//
// Used by topt_initialize to configure environment and tagging.
typedef struct {
    char* language;
    char* runtime_name;
    char* runtime_version;
    char* working_directory;
    topt_KeyValueArray* environment_variables;
	topt_KeyValueArray* global_tags;
	Bool use_mock_tracer;
	// Unused fields
	void* unused01;
	void* unused02;
	void* unused03;
	void* unused04;
	void* unused05;
} topt_InitOptions;

// topt_UnixTime represents a point in time using seconds and nanoseconds since the Unix epoch.
// Fields:
//   - sec: Seconds since the Unix epoch.
//   - nsec: Nanoseconds since the last whole second.
typedef struct {
    Uint64 sec;
    Uint64 nsec;
} topt_UnixTime;

// topt_TestCloseOptions specifies how to close a test, including its outcome and optional finishing time.
// Fields:
//   - status: A topt_TestStatus indicating pass/fail/skip.
//   - finish_time: A pointer to a topt_UnixTime for when the test ended.
//   - skip_reason: A string describing why the test was skipped if status is skip.
//   - unused01 ... unused05: Reserved fields for future use.
typedef struct {
	topt_TestStatus status;
	topt_UnixTime* finish_time;
	char* skip_reason;
	// Unused fields
	void* unused01;
	void* unused02;
	void* unused03;
	void* unused04;
	void* unused05;
} topt_TestCloseOptions;

// topt_SettingsEarlyFlakeDetectionSlowRetries defines threshold-based retry settings for "slow" tests in early flake detection.
// Fields:
//   - ten_s: Retries triggered if test runs longer than 10 seconds.
//   - thirty_s: Retries triggered if test runs longer than 30 seconds.
//   - five_m: Retries triggered if test runs longer than 5 minutes.
//   - five_s: Retries triggered if test runs longer than 5 seconds.
//
// These fields help configure how the system identifies flakiness based on test durations.
typedef struct {
	int ten_s;
	int thirty_s;
	int five_m;
	int five_s;
} topt_SettingsEarlyFlakeDetectionSlowRetries;

// topt_SettingsEarlyFlakeDetection holds configuration for early flake detection features.
// Fields:
//   - enabled: Boolean indicating if early flake detection is on.
//   - slow_test_retries: topt_SettingsEarlyFlakeDetectionSlowRetries structure defining retry rules for slow tests.
//   - faulty_session_threshold: An integer threshold for deciding when a session is considered faulty.
typedef struct {
	Bool enabled;
	topt_SettingsEarlyFlakeDetectionSlowRetries slow_test_retries;
	int faulty_session_threshold;
} topt_SettingsEarlyFlakeDetection;

// topt_SettingsTestManagement holds configuration for test management features.
// Fields:
//  - enabled: Boolean indicating if test management is enabled.
//  - attempt_to_fix_retries: Number of retries to attempt fixing a test.
typedef struct {
	Bool enabled;
	int attempt_to_fix_retries;
} topt_SettingsTestManagement;

// topt_SettingsResponse returns the library’s current configuration and feature toggles.
// Fields:
//   - code_coverage: Boolean indicating if code coverage is enabled.
//   - early_flake_detection: Contains early flake detection parameters.
//   - flaky_test_retries_enabled: Boolean indicating if flaky test retries are enabled.
//   - itr_enabled: Boolean indicating if Intelligent Test Runner (ITR) is enabled.
//   - require_git: Boolean indicating if Git repository context is required.
//   - tests_skipping: Boolean indicating if test skipping is enabled.
//	 - known_tests_enabled: Boolean indicating if known tests are enabled.
//   - test_management: Contains test management settings.
//   - unused01 ... unused05: Reserved for future use.
typedef struct {
	Bool code_coverage;
	topt_SettingsEarlyFlakeDetection early_flake_detection;
	Bool flaky_test_retries_enabled;
	Bool itr_enabled;
	Bool require_git;
	Bool tests_skipping;
	Bool known_tests_enabled;
	topt_SettingsTestManagement test_management;
	// Unused fields
	void* unused01;
	void* unused02;
	void* unused03;
	void* unused04;
	void* unused05;
} topt_SettingsResponse;

// topt_FlakyTestRetriesSettings contains the configuration for how many times flaky tests should be retried.
// Fields:
//   - retry_count: Number of retries for a given flaky test.
//   - total_retry_count: The cumulative number of retries permitted across all flaky tests.
typedef struct {
	int retry_count;
	int total_retry_count;
} topt_FlakyTestRetriesSettings;

// topt_KnownTest represents a known test within the system.
// Fields:
//   - module_name: The name of the test module.
//   - suite_name: The name of the test suite.
//   - test_name: The name of the test itself.
typedef struct {
	char* module_name;
	char* suite_name;
	char* test_name;
} topt_KnownTest;

// topt_KnownTestArray is a collection of known tests.
// Fields:
//   - data: Pointer to an array of topt_KnownTest.
//   - len: The length of the array.
//
// Used by topt_get_known_tests to return a list of known tests.
typedef struct {
	topt_KnownTest* data;
	size_t len;
} topt_KnownTestArray;

// topt_SkippableTest defines a test that can be skipped.
// Fields:
//   - suite_name: The suite containing the skippable test.
//   - test_name: The test that can be skipped.
//   - parameters: Optional parameters for the test.
//   - custom_configurations_json: JSON string containing custom configurations for skipping logic.
typedef struct {
	char* suite_name;
	char* test_name;
	char* parameters;
	char* custom_configurations_json;
} topt_SkippableTest;

// topt_SkippableTestArray is a collection of skippable tests.
// Fields:
//   - data: Pointer to an array of topt_SkippableTest.
//   - len: The length of the array.
//
// Returned by topt_get_skippable_tests.
typedef struct {
	topt_SkippableTest* data;
	size_t len;
} topt_SkippableTestArray;

// topt_TestCoverageFile represents coverage data for a single source file.
// Fields:
//   - filename: The name of the source file.
//   - bitmap: A pointer to a coverage bitmap representing which lines were covered.
//   - bitmap_len: The size of the bitmap in bytes.
typedef struct {
	char* filename;
	void* bitmap;
	size_t bitmap_len;
} topt_TestCoverageFile;

// topt_TestCoverage holds coverage information for a single test execution.
// Fields:
//   - session_id: The session this test belongs to.
//   - suite_id: The suite this test belongs to.
//   - test_id: The test’s own identifier.
//   - files: An array of topt_TestCoverageFile representing coverage data for multiple files.
//   - files_len: The number of files in the files array.
typedef struct {
	topt_SessionId session_id;
	topt_SuiteId suite_id;
	topt_TestId test_id;
	topt_TestCoverageFile* files;
	size_t files_len;
} topt_TestCoverage;

// topt_TestManagementTestProperties holds properties for a test managed by the test management system.
// Fields:
//   - module_name: The name of the module containing the test.
//   - suite_name: The name of the suite containing the test.
//   - test_name: The name of the test.
//   - quarantined: A Bool indicating if the test is quarantined.
//   - disabled: A Bool indicating if the test is disabled.
//   - attempt_to_fix: A Bool indicating if the test should be attempted to fix.
typedef struct {
	char* module_name;
	char* suite_name;
	char* test_name;
	Bool quarantined;
	Bool disabled;
	Bool attempt_to_fix;
} topt_TestManagementTestProperties;

// topt_TestManagementTestPropertiesArray is a collection of test properties from the Test Management feature.
// Fields:
//   - data: Pointer to an array of topt_TestManagementTestProperties.
//   - len: The length of the array.
// Used by topt_get_test_management_tests to return a list of test properties.
typedef struct {
	topt_TestManagementTestProperties* data;
	size_t len;
} topt_TestManagementTestPropertiesArray;

// topt_SpanStartOptions provides configuration for starting a new span (a timing/trace operation).
// Fields:
//   - operation_name: The name of the operation represented by the span.
//   - service_name: The name of the service this span belongs to, optional.
//   - resource_name: The resource name (e.g., specific endpoint or function), optional.
//   - span_type: A string indicating the type of the span (e.g., "web", "db"), optional.
//   - start_time: Pointer to a topt_UnixTime marking when the span started, optional.
//   - string_tags: Pointer to a topt_KeyValueArray of string tags for the span, optional.
//   - number_tags: Pointer to a topt_KeyNumberArray of numeric tags for the span, optional.
//
// Used by topt_span_create to define span metadata.
typedef struct {
	char* operation_name;
	char* service_name;
	char* resource_name;
	char* span_type;
	topt_UnixTime* start_time;
    topt_KeyValueArray* string_tags;
	topt_KeyNumberArray* number_tags;
} topt_SpanStartOptions;

// topt_SpanResult is returned when a new span is created.
// Fields:
//   - span_id: The ID of the newly created span.
//   - valid: A Bool indicating whether the creation was successful.
typedef struct {
	topt_TslvId span_id;
	Bool valid;
} topt_SpanResult;

// topt_MockSpan represents a mock span for testing purposes.
// Fields:
//   - span_id: The ID of the span.
//   - trace_id: The ID of the trace this span belongs to.
//   - parent_span_id: The ID of the parent span.
//   - start_time: The time when the span started.
//   - finish_time: The time when the span finished.
//   - operation_name: The name of the operation represented by the span.
//   - string_tags: An array of string tags for the span.
//   - number_tags: An array of numeric tags for the span.
typedef struct {
 	topt_TslvId span_id;
	topt_TslvId trace_id;
	topt_TslvId parent_span_id;
	topt_UnixTime start_time;
	topt_UnixTime finish_time;
	char* operation_name;
    topt_KeyValueArray string_tags;
	topt_KeyNumberArray number_tags;
} topt_MockSpan;

// topt_MockSpanArray is an array of mock spans.
// Fields:
//   - data: Pointer to an array of topt_MockSpan.
//   - len: The length of the array.
typedef struct {
	topt_MockSpan* data;
	size_t len;
} topt_MockSpanArray;
*/
import "C"
import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	civisibility "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/net"
)

// *******************************************************************************************************************
// Struct sizes
// *******************************************************************************************************************

const (
	topt_KeyValuePair_Size                 = C.size_t(unsafe.Sizeof(C.topt_KeyValuePair{}))
	topt_KeyNumberPair_Size                = C.size_t(unsafe.Sizeof(C.topt_KeyNumberPair{}))
	topt_KnownTest_Size                    = C.size_t(unsafe.Sizeof(C.topt_KnownTest{}))
	topt_SkippableTest_Size                = C.size_t(unsafe.Sizeof(C.topt_SkippableTest{}))
	topt_TestCoverageFile_Size             = C.size_t(unsafe.Sizeof(C.topt_TestCoverageFile{}))
	topt_TestCoverage_Size                 = C.size_t(unsafe.Sizeof(C.topt_TestCoverage{}))
	topt_TestManagementTestProperties_Size = C.size_t(unsafe.Sizeof(C.topt_TestManagementTestProperties{}))
	topt_MockSpan_Size                     = C.size_t(unsafe.Sizeof(C.topt_MockSpan{}))
)

type (
	// spanContainer represents a span and its context.
	spanContainer struct {
		span ddtracer.Span
		ctx  context.Context
	}

	exportData struct {
		hasInitialized atomic.Bool // indicate if the library has been initialized
		canShutdown    atomic.Bool // indicate if the library can be shut down
		client         net.Client  // client to send code coverage payloads

		sessionMutex sync.RWMutex                        // mutex to protect the session map
		sessions     map[uint64]civisibility.TestSession // map of test sessions
		moduleMutex  sync.RWMutex                        // mutex to protect the modules map
		modules      map[uint64]civisibility.TestModule  // map of test modules
		suiteMutex   sync.RWMutex                        // mutex to protect the suites map
		suites       map[uint64]civisibility.TestSuite   // map of test suites
		testMutex    sync.RWMutex                        // mutex to protect the tests map
		tests        map[uint64]civisibility.Test        // map of test spans
		spanMutex    sync.RWMutex                        // mutex to protect the spans map
		spans        map[uint64]spanContainer            // map of spans

		mockTracer mocktracer.Tracer // mock tracer for testing
	}
)

var exports = exportData{
	client:   net.NewClientForCodeCoverage(),
	sessions: make(map[uint64]civisibility.TestSession),
	modules:  make(map[uint64]civisibility.TestModule),
	suites:   make(map[uint64]civisibility.TestSuite),
	tests:    make(map[uint64]civisibility.Test),
	spans:    make(map[uint64]spanContainer),
}

// *******************************************************************************************************************
// Utils
// *******************************************************************************************************************

// getUnixTime converts a C.UnixTime to Go's time.Time.
// If unixTime is nil, returns time.Now() as a fallback.
func getUnixTime(unixTime *C.topt_UnixTime) time.Time {
	// If pointer is nil, provide a fallback time.
	if unixTime == nil {
		return time.Now()
	}
	return time.Unix(int64(unixTime.sec), int64(unixTime.nsec))
}

// toUnixTime converts a Go time.Time to C.UnixTime.
func toUnixTime(t time.Time) C.topt_UnixTime {
	return C.topt_UnixTime{
		sec:  C.Uint64(t.Unix()),
		nsec: C.Uint64(t.Nanosecond()),
	}
}

// toBool converts a Go bool to a C.Bool (0 or 1).
func toBool(value bool) C.Bool {
	if value {
		return C.Bool(1)
	}
	return C.Bool(0)
}

// fromBool converts a C.Bool (0 or 1) to a Go bool.
func fromBool(value C.Bool) bool {
	return value != 0
}

// *******************************************************************************************************************
// General
// *******************************************************************************************************************

type (
	// ciTestCovPayload represents a test code coverage payload specifically designed for CI Visibility events.
	ciTestCovPayload struct {
		Version   int32                `json:"version"`   // Version of the payload
		Coverages []ciTestCoverageData `json:"coverages"` // list of coverages
	}

	// ciTestCoverageData represents the coverage data for a single test.
	ciTestCoverageData struct {
		SessionID uint64               `json:"test_session_id"` // identifier of this session
		SuiteID   uint64               `json:"test_suite_id"`   // identifier of the suite
		SpanID    uint64               `json:"span_id"`         // identifier of this test
		Files     []ciTestCoverageFile `json:"files"`           // list of files covered
	}

	// ciTestCoverageFile represents the coverage data for a single file.
	ciTestCoverageFile struct {
		FileName string `json:"filename"` // name of the file
		Bitmap   []byte `json:"bitmap"`   // coverage bitmap
	}
)

// topt_initialize initializes the library with the given runtime and environment options.
//
// Parameters:
//   - options: A struct of type topt_InitOptions containing initialization configuration such as language, runtime info, working directory, environment variables, and global tags.
//
// Returns:
//   - C.Bool: Returns true if the library was successfully initialized, and false if it was already initialized or initialization failed.
//
// This function should be called before any other topt_ functions. If called multiple times, only the first call will have effect. Subsequent calls will return false.
//
// Example usage:
//
//	topt_initialize(myOptions)
//
//export topt_initialize
func topt_initialize(options C.topt_InitOptions) C.Bool {
	if exports.hasInitialized.Swap(true) {
		return toBool(false)
	}

	exports.canShutdown.Store(true)
	tags := make(map[string]string)
	if options.environment_variables != nil {
		for i := C.size_t(0); i < options.environment_variables.len; i++ {
			keyValue := (*C.topt_KeyValuePair)(unsafe.Add(unsafe.Pointer(options.environment_variables.data), i*topt_KeyValuePair_Size))
			if keyValue.key == nil {
				continue
			}
			os.Setenv(C.GoString(keyValue.key), C.GoString(keyValue.value))
		}
	}
	if options.working_directory != nil {
		wd := C.GoString(options.working_directory)
		if wd != "" {
			if currentDir, err := os.Getwd(); err == nil {
				defer func() {
					os.Chdir(currentDir)
				}()
			}
			os.Chdir(wd)
		}
	}
	if options.runtime_name != nil {
		tags[constants.RuntimeName] = C.GoString(options.runtime_name)
	}
	if options.runtime_version != nil {
		tags[constants.RuntimeVersion] = C.GoString(options.runtime_version)
	}
	if options.language != nil {
		tags["language"] = C.GoString(options.language)
	} else {
		tags["language"] = "native"
	}
	if options.global_tags != nil {
		for i := C.size_t(0); i < options.global_tags.len; i++ {
			keyValue := (*C.topt_KeyValuePair)(unsafe.Add(unsafe.Pointer(options.global_tags.data), i*topt_KeyValuePair_Size))
			if keyValue.key == nil {
				continue
			}
			tags[C.GoString(keyValue.key)] = C.GoString(keyValue.value)
		}
	}

	utils.AddCITagsMap(tags)
	if fromBool(options.use_mock_tracer) {
		exports.mockTracer = civisibility.InitializeCIVisibilityMock()
	} else {
		civisibility.EnsureCiVisibilityInitialization()
	}

	return toBool(true)
}

// topt_shutdown gracefully shuts down the library.
//
// Returns:
//   - C.Bool: Returns true if the library was successfully shut down, and false if it was already shut down or never initialized.
//
// After calling this function, the library is no longer safe to use. All ongoing sessions, modules, suites, tests, and spans will be considered closed.
//
//export topt_shutdown
func topt_shutdown() C.Bool {
	if !exports.canShutdown.Swap(false) {
		return toBool(false)
	}
	civisibility.ExitCiVisibility()
	return toBool(true)
}

// topt_get_settings retrieves the current configuration and feature flags of the library.
//
// Returns:
//   - topt_SettingsResponse: A struct containing various settings such as code coverage enablement, early flake detection parameters, flaky test retries, and other integration-related flags.
//
// If no settings are available or if retrieval fails, it returns default values (mostly disabled).
//
//export topt_get_settings
func topt_get_settings() C.topt_SettingsResponse {
	settings := civisibility.GetSettings()
	if settings == nil {
		return C.topt_SettingsResponse{
			code_coverage: toBool(false),
			early_flake_detection: C.topt_SettingsEarlyFlakeDetection{
				enabled: toBool(false),
				slow_test_retries: C.topt_SettingsEarlyFlakeDetectionSlowRetries{
					ten_s:    0,
					thirty_s: 0,
					five_m:   0,
					five_s:   0,
				},
				faulty_session_threshold: 0,
			},
			flaky_test_retries_enabled: toBool(false),
			itr_enabled:                toBool(false),
			require_git:                toBool(false),
			tests_skipping:             toBool(false),
			known_tests_enabled:        toBool(false),
			test_management: C.topt_SettingsTestManagement{
				enabled:                toBool(false),
				attempt_to_fix_retries: 0,
			},
		}
	}

	return C.topt_SettingsResponse{
		code_coverage: toBool(settings.CodeCoverage),
		early_flake_detection: C.topt_SettingsEarlyFlakeDetection{
			enabled: toBool(settings.EarlyFlakeDetection.Enabled),
			slow_test_retries: C.topt_SettingsEarlyFlakeDetectionSlowRetries{
				ten_s:    C.int(settings.EarlyFlakeDetection.SlowTestRetries.TenS),
				thirty_s: C.int(settings.EarlyFlakeDetection.SlowTestRetries.ThirtyS),
				five_m:   C.int(settings.EarlyFlakeDetection.SlowTestRetries.FiveM),
				five_s:   C.int(settings.EarlyFlakeDetection.SlowTestRetries.FiveS),
			},
			faulty_session_threshold: C.int(settings.EarlyFlakeDetection.FaultySessionThreshold),
		},
		flaky_test_retries_enabled: toBool(settings.FlakyTestRetriesEnabled),
		itr_enabled:                toBool(settings.ItrEnabled),
		require_git:                toBool(settings.RequireGit),
		tests_skipping:             toBool(settings.TestsSkipping),
		known_tests_enabled:        toBool(settings.KnownTestsEnabled),
		test_management: C.topt_SettingsTestManagement{
			enabled:                toBool(settings.TestManagement.Enabled),
			attempt_to_fix_retries: C.int(settings.TestManagement.AttemptToFixRetries),
		},
	}
}

// topt_get_flaky_test_retries_settings retrieves the configuration for flaky test retries.
//
// Returns:
//   - topt_FlakyTestRetriesSettings: Contains the retry count and total retry count for flaky tests.
//
// If no configuration is available, returns zeroed values.
//
//export topt_get_flaky_test_retries_settings
func topt_get_flaky_test_retries_settings() C.topt_FlakyTestRetriesSettings {
	settings := civisibility.GetFlakyRetriesSettings()
	if settings == nil {
		return C.topt_FlakyTestRetriesSettings{
			retry_count:       0,
			total_retry_count: 0,
		}
	}

	return C.topt_FlakyTestRetriesSettings{
		retry_count:       C.int(settings.RetryCount),
		total_retry_count: C.int(settings.TotalRetryCount),
	}
}

// topt_get_known_tests returns an array of known tests.
//
// Returns:
//   - topt_KnownTestArray: A struct holding a dynamically allocated array of topt_KnownTest elements along with its length.
//
// Use topt_free_known_tests to free the allocated memory.
//
//export topt_get_known_tests
func topt_get_known_tests() C.topt_KnownTestArray {
	var knownTests []C.topt_KnownTest
	for moduleName, module := range civisibility.GetKnownTests().Tests {
		for suiteName, suite := range module {
			for _, testName := range suite {
				knownTests = append(knownTests, C.topt_KnownTest{
					module_name: C.CString(moduleName),
					suite_name:  C.CString(suiteName),
					test_name:   C.CString(testName),
				})
			}
		}
	}

	cKnownTests := unsafe.Pointer(C.malloc(C.size_t(len(knownTests)) * topt_KnownTest_Size))
	for i, knownTest := range knownTests {
		*(*C.topt_KnownTest)(unsafe.Add(cKnownTests, C.size_t(i)*topt_KnownTest_Size)) = knownTest
	}

	return C.topt_KnownTestArray{
		data: (*C.topt_KnownTest)(cKnownTests),
		len:  C.size_t(len(knownTests)),
	}
}

// topt_free_known_tests frees the memory allocated by topt_get_known_tests.
//
// Parameters:
//   - knownTests: The topt_KnownTestArray previously returned by topt_get_known_tests.
//
// This function should be called after you are done using the known tests array to avoid memory leaks.
//
//export topt_free_known_tests
func topt_free_known_tests(knownTests C.topt_KnownTestArray) {
	if knownTests.data != nil {
		for i := C.size_t(0); i < knownTests.len; i++ {
			knownTest := *(*C.topt_KnownTest)(unsafe.Add(unsafe.Pointer(knownTests.data), i*topt_KnownTest_Size))
			C.free(unsafe.Pointer(knownTest.module_name))
			C.free(unsafe.Pointer(knownTest.suite_name))
			C.free(unsafe.Pointer(knownTest.test_name))
		}
		C.free(unsafe.Pointer(knownTests.data))
	}
}

// topt_get_skippable_tests retrieves an array of tests that should be skipped, including their parameters and any custom configurations.
//
// Returns:
//   - topt_SkippableTestArray: A struct containing a dynamically allocated array of topt_SkippableTest along with its length.
//
// Use topt_free_skippable_tests to free the allocated memory.
//
//export topt_get_skippable_tests
func topt_get_skippable_tests() C.topt_SkippableTestArray {
	var skippableTests []C.topt_SkippableTest
	for suite_name, sSuites := range civisibility.GetSkippableTests() {
		for test_name, sTests := range sSuites {
			for _, sTest := range sTests {
				var custom_config string
				if sTest.Configurations.Custom != nil {
					jsonBytes, _ := json.Marshal(sTest.Configurations.Custom)
					custom_config = string(jsonBytes)
				}

				skippableTests = append(skippableTests, C.topt_SkippableTest{
					suite_name:                 C.CString(suite_name),
					test_name:                  C.CString(test_name),
					parameters:                 C.CString(sTest.Parameters),
					custom_configurations_json: C.CString(custom_config),
				})
			}
		}
	}

	cSkippableTests := unsafe.Pointer(C.malloc(C.size_t(len(skippableTests)) * topt_SkippableTest_Size))
	for i, skippableTest := range skippableTests {
		*(*C.topt_SkippableTest)(unsafe.Add(cSkippableTests, C.size_t(i)*topt_SkippableTest_Size)) = skippableTest
	}

	return C.topt_SkippableTestArray{
		data: (*C.topt_SkippableTest)(cSkippableTests),
		len:  C.size_t(len(skippableTests)),
	}
}

// topt_free_skippable_tests frees the memory allocated by topt_get_skippable_tests.
//
// Parameters:
//   - skippableTests: The topt_SkippableTestArray previously returned by topt_get_skippable_tests.
//
// This function should be called after you are done using the skippable tests array to avoid memory leaks.
//
//export topt_free_skippable_tests
func topt_free_skippable_tests(skippableTests C.topt_SkippableTestArray) {
	if skippableTests.data != nil {
		for i := C.size_t(0); i < skippableTests.len; i++ {
			skippableTest := *(*C.topt_SkippableTest)(unsafe.Add(unsafe.Pointer(skippableTests.data), i*topt_SkippableTest_Size))
			C.free(unsafe.Pointer(skippableTest.suite_name))
			C.free(unsafe.Pointer(skippableTest.test_name))
			C.free(unsafe.Pointer(skippableTest.parameters))
			C.free(unsafe.Pointer(skippableTest.custom_configurations_json))
		}
		C.free(unsafe.Pointer(skippableTests.data))
	}
}

// topt_send_code_coverage_payload sends one or more code coverage payloads to the backend.
//
// Parameters:
//   - coverages: A pointer to an array of topt_TestCoverage structs describing code coverage data for multiple tests.
//   - coverages_length: The number of elements in the coverages array.
//
// This function will assemble the data into a payload and send it to the configured backend. It does not return a status; errors are handled internally.
//
//export topt_send_code_coverage_payload
func topt_send_code_coverage_payload(coverages *C.topt_TestCoverage, coverages_length C.size_t) {
	if coverages == nil || coverages_length == 0 {
		return
	}

	coveragePayload := ciTestCovPayload{
		Version: 2,
	}
	for i := C.size_t(0); i < coverages_length; i++ {
		coverage := *(*C.topt_TestCoverage)(unsafe.Add(unsafe.Pointer(coverages), i*topt_TestCoverage_Size))
		coverageData := ciTestCoverageData{
			SessionID: uint64(coverage.session_id),
			SuiteID:   uint64(coverage.suite_id),
			SpanID:    uint64(coverage.test_id),
		}
		for j := C.size_t(0); j < coverage.files_len; j++ {
			file := *(*C.topt_TestCoverageFile)(unsafe.Add(unsafe.Pointer(coverage.files), j*topt_TestCoverageFile_Size))
			coverageFile := ciTestCoverageFile{FileName: C.GoString(file.filename)}
			coverageFile.FileName = utils.GetRelativePathFromCITagsSourceRoot(coverageFile.FileName)
			if file.bitmap_len > 0 && file.bitmap != nil {
				coverageFile.Bitmap = C.GoBytes(unsafe.Pointer(file.bitmap), C.int(file.bitmap_len))
			}
			coverageData.Files = append(coverageData.Files, coverageFile)
		}
		coveragePayload.Coverages = append(coveragePayload.Coverages, coverageData)
	}

	// Create a new buffer to encode the coverage payload in MessagePack format
	encodedBuf := new(bytes.Buffer)
	jsonbytes, err := json.Marshal(&coveragePayload)
	if err == nil {
		encodedBuf.Write(jsonbytes)
		exports.client.SendCoveragePayloadWithFormat(encodedBuf, net.FormatJSON)
	}
}

// topt_get_test_management_tests retrieves a list of tests managed by the test management system.
//
// Returns:
//   - topt_TestManagementTestPropertiesArray: A struct containing a dynamically allocated array of topt_TestManagementTestProperties along with its length.
//
// Use topt_free_test_management_tests to free the allocated memory.
//
//export topt_get_test_management_tests
func topt_get_test_management_tests() C.topt_TestManagementTestPropertiesArray {
	var testProperties []C.topt_TestManagementTestProperties
	attrs := civisibility.GetTestManagementTestsData()
	if attrs.Modules != nil {
		for moduleName, module := range attrs.Modules {
			if module.Suites == nil {
				continue
			}
			for suiteName, suite := range module.Suites {
				if suite.Tests == nil {
					continue
				}
				for testName, test := range suite.Tests {
					testProperties = append(testProperties, C.topt_TestManagementTestProperties{
						module_name:    C.CString(moduleName),
						suite_name:     C.CString(suiteName),
						test_name:      C.CString(testName),
						quarantined:    toBool(test.Properties.Quarantined),
						disabled:       toBool(test.Properties.Disabled),
						attempt_to_fix: toBool(test.Properties.AttemptToFix),
					})
				}
			}
		}
	}

	cTestProperties := unsafe.Pointer(C.malloc(C.size_t(len(testProperties)) * topt_TestManagementTestProperties_Size))
	for i, testProperty := range testProperties {
		*(*C.topt_TestManagementTestProperties)(unsafe.Add(cTestProperties, C.size_t(i)*topt_TestManagementTestProperties_Size)) = testProperty
	}

	return C.topt_TestManagementTestPropertiesArray{
		data: (*C.topt_TestManagementTestProperties)(cTestProperties),
		len:  C.size_t(len(testProperties)),
	}
}

// topt_free_test_management_tests frees the memory allocated by topt_get_test_management_tests.
//
// Parameters:
//   - testProperties: The topt_TestManagementTestPropertiesArray previously returned by topt_get_test_management_tests.
//
// This function should be called after you are done using the test management tests array to avoid memory leaks.
//
//export topt_free_test_management_tests
func topt_free_test_management_tests(testProperties C.topt_TestManagementTestPropertiesArray) {
	if testProperties.data != nil {
		for i := C.size_t(0); i < testProperties.len; i++ {
			testProperty := *(*C.topt_TestManagementTestProperties)(unsafe.Add(unsafe.Pointer(testProperties.data), i*topt_TestManagementTestProperties_Size))
			C.free(unsafe.Pointer(testProperty.module_name))
			C.free(unsafe.Pointer(testProperty.suite_name))
			C.free(unsafe.Pointer(testProperty.test_name))
		}
		C.free(unsafe.Pointer(testProperties.data))
	}
}

// *******************************************************************************************************************
// Sessions
// *******************************************************************************************************************

func getSession(session_id C.topt_SessionId) (civisibility.TestSession, bool) {
	sId := uint64(session_id)
	if sId == 0 {
		return nil, false
	}
	exports.sessionMutex.RLock()
	defer exports.sessionMutex.RUnlock()
	session, ok := exports.sessions[sId]
	return session, ok
}

// topt_session_create creates a new test session.
//
// Parameters:
//   - framework: Optional name of the testing framework.
//   - framework_version: Optional version of the testing framework.
//   - start_time: Optional pointer to a topt_UnixTime representing the session start time. If nil, current time is used.
//
// Returns:
//   - topt_SessionResult: A struct containing the session_id and a boolean indicating success.
//
// If successful, the session_id can be used with subsequent session-related functions. Each session should eventually be closed with topt_session_close.
//
//export topt_session_create
func topt_session_create(framework *C.char, framework_version *C.char, start_time *C.topt_UnixTime) C.topt_SessionResult {
	var sessionOptions []civisibility.TestSessionStartOption
	if framework != nil {
		sessionOptions = append(sessionOptions, civisibility.WithTestSessionFramework(C.GoString(framework), C.GoString(framework_version)))
	}
	if start_time != nil {
		sessionOptions = append(sessionOptions, civisibility.WithTestSessionStartTime(getUnixTime(start_time)))
	}

	session := civisibility.CreateTestSession(sessionOptions...)
	id := session.SessionID()

	exports.sessionMutex.Lock()
	defer exports.sessionMutex.Unlock()
	exports.sessions[id] = session
	return C.topt_SessionResult{session_id: C.topt_SessionId(id), valid: toBool(true)}
}

// topt_session_close closes an existing test session.
//
// Parameters:
//   - session_id: The ID of the session to close.
//   - exit_code: The exit code of the overall test run (e.g., 0 for success).
//   - finish_time: Optional pointer to a topt_UnixTime representing when the session ended. If nil, current time is used.
//
// Returns:
//   - C.Bool: True if the session was found and closed, false otherwise.
//
// After this call, the session_id is no longer valid.
//
//export topt_session_close
func topt_session_close(session_id C.topt_SessionId, exit_code C.int, finish_time *C.topt_UnixTime) C.Bool {
	if session, ok := getSession(session_id); ok {
		if finish_time != nil {
			session.Close(int(exit_code), civisibility.WithTestSessionFinishTime(getUnixTime(finish_time)))
		} else {
			session.Close(int(exit_code))
		}

		exports.sessionMutex.Lock()
		defer exports.sessionMutex.Unlock()
		delete(exports.sessions, uint64(session_id))
		return toBool(true)
	}

	return toBool(false)
}

// topt_session_set_string_tag sets a string tag on a test session.
//
// Parameters:
//   - session_id: The ID of the session.
//   - key: The string tag key.
//   - value: The string tag value.
//
// Returns:
//   - C.Bool: True if the tag was set successfully, false if the session was not found or key was nil.
//
// Tags add metadata to the session.
//
//export topt_session_set_string_tag
func topt_session_set_string_tag(session_id C.topt_SessionId, key *C.char, value *C.char) C.Bool {
	if key == nil {
		return toBool(false)
	}
	if session, ok := getSession(session_id); ok {
		session.SetTag(C.GoString(key), C.GoString(value))
		return toBool(true)
	}
	return toBool(false)
}

// topt_session_set_number_tag sets a numerical tag on a test session.
//
// Parameters:
//   - session_id: The ID of the session.
//   - key: The tag key.
//   - value: A double representing the tag value.
//
// Returns:
//   - C.Bool: True if the tag was set successfully, false otherwise.
//
// Similar to string tags, number tags provide numeric metadata on the session.
//
//export topt_session_set_number_tag
func topt_session_set_number_tag(session_id C.topt_SessionId, key *C.char, value C.double) C.Bool {
	if key == nil {
		return toBool(false)
	}
	if session, ok := getSession(session_id); ok {
		session.SetTag(C.GoString(key), float64(value))
		return toBool(true)
	}
	return toBool(false)
}

// topt_session_set_error marks a session as having encountered an error.
//
// Parameters:
//   - session_id: The ID of the session.
//   - error_type: A string describing the type/kind of error.
//   - error_message: A string containing a descriptive error message.
//   - error_stacktrace: A string containing the stacktrace or contextual error details.
//
// Returns:
//   - C.Bool: True if the error was recorded successfully, false otherwise.
//
// Use this to indicate that something went wrong during the session.
//
//export topt_session_set_error
func topt_session_set_error(session_id C.topt_SessionId, error_type *C.char, error_message *C.char, error_stacktrace *C.char) C.Bool {
	if session, ok := getSession(session_id); ok {
		session.SetError(civisibility.WithErrorInfo(C.GoString(error_type), C.GoString(error_message), C.GoString(error_stacktrace)))
		return toBool(true)
	}
	return toBool(false)
}

// *******************************************************************************************************************
// Modules
// *******************************************************************************************************************

func getModule(module_id C.topt_ModuleId) (civisibility.TestModule, bool) {
	mId := uint64(module_id)
	if mId == 0 {
		return nil, false
	}
	exports.moduleMutex.RLock()
	defer exports.moduleMutex.RUnlock()
	module, ok := exports.modules[mId]
	return module, ok
}

// topt_module_create creates or retrieves a test module within an existing session.
//
// Parameters:
//   - session_id: The ID of the parent session.
//   - name: The name of the test module.
//   - framework: Optional name of the test framework.
//   - framework_version: Optional version of the test framework.
//   - start_time: Optional pointer to a topt_UnixTime for the module start time.
//
// Returns:
//   - topt_ModuleResult: Contains the module_id and a validity flag.
//
// The created or retrieved module is identified by name and can be closed later with topt_module_close.
//
//export topt_module_create
func topt_module_create(session_id C.topt_SessionId, name *C.char, framework *C.char, framework_version *C.char, start_time *C.topt_UnixTime) C.topt_ModuleResult {
	if name == nil {
		return C.topt_ModuleResult{module_id: C.topt_ModuleId(0), valid: toBool(false)}
	}

	if session, ok := getSession(session_id); ok {
		var moduleOptions []civisibility.TestModuleStartOption
		if framework != nil {
			moduleOptions = append(moduleOptions, civisibility.WithTestModuleFramework(C.GoString(framework), C.GoString(framework_version)))
		}
		if start_time != nil {
			moduleOptions = append(moduleOptions, civisibility.WithTestModuleStartTime(getUnixTime(start_time)))
		}

		module := session.GetOrCreateModule(C.GoString(name), moduleOptions...)
		id := module.ModuleID()

		exports.moduleMutex.Lock()
		defer exports.moduleMutex.Unlock()
		exports.modules[id] = module
		return C.topt_ModuleResult{module_id: C.topt_ModuleId(id), valid: toBool(true)}
	}

	return C.topt_ModuleResult{module_id: C.topt_ModuleId(0), valid: toBool(false)}
}

// topt_module_close closes a test module.
//
// Parameters:
//   - module_id: The ID of the module to close.
//   - finish_time: Optional pointer to a topt_UnixTime representing the module end time.
//
// Returns:
//   - C.Bool: True if the module was found and closed, false otherwise.
//
//export topt_module_close
func topt_module_close(module_id C.topt_ModuleId, finish_time *C.topt_UnixTime) C.Bool {
	if module, ok := getModule(module_id); ok {
		if finish_time != nil {
			module.Close(civisibility.WithTestModuleFinishTime(getUnixTime(finish_time)))
		} else {
			module.Close()
		}

		exports.moduleMutex.Lock()
		defer exports.moduleMutex.Unlock()
		delete(exports.modules, uint64(module_id))
		return toBool(true)
	}

	return toBool(false)
}

// topt_module_set_string_tag adds a string tag to a test module.
//
// Parameters:
//   - module_id: The module's ID.
//   - key: Tag key string.
//   - value: Tag value string.
//
// Returns:
//   - C.Bool: True if successful, false otherwise.
//
//export topt_module_set_string_tag
func topt_module_set_string_tag(module_id C.topt_ModuleId, key *C.char, value *C.char) C.Bool {
	if key == nil {
		return toBool(false)
	}
	if module, ok := getModule(module_id); ok {
		module.SetTag(C.GoString(key), C.GoString(value))
		return toBool(true)
	}
	return toBool(false)
}

// topt_module_set_number_tag adds a numeric tag to a test module.
//
// Parameters:
//   - module_id: The module's ID.
//   - key: Tag key string.
//   - value: A double representing the numeric value.
//
// Returns:
//   - C.Bool: True if successful, false otherwise.
//
//export topt_module_set_number_tag
func topt_module_set_number_tag(module_id C.topt_ModuleId, key *C.char, value C.double) C.Bool {
	if key == nil {
		return toBool(false)
	}
	if module, ok := getModule(module_id); ok {
		module.SetTag(C.GoString(key), float64(value))
		return toBool(true)
	}
	return toBool(false)
}

// topt_module_set_error records an error for the specified test module.
//
// Parameters:
//   - module_id: The module's ID.
//   - error_type: Error classification or type.
//   - error_message: Descriptive message of the error.
//   - error_stacktrace: Stacktrace or additional error context.
//
// Returns:
//   - C.Bool: True if the error was recorded, false otherwise.
//
//export topt_module_set_error
func topt_module_set_error(module_id C.topt_ModuleId, error_type *C.char, error_message *C.char, error_stacktrace *C.char) C.Bool {
	if module, ok := getModule(module_id); ok {
		module.SetError(civisibility.WithErrorInfo(C.GoString(error_type), C.GoString(error_message), C.GoString(error_stacktrace)))
		return toBool(true)
	}
	return toBool(false)
}

// *******************************************************************************************************************
// Suites
// *******************************************************************************************************************

func getSuite(suite_id C.topt_SuiteId) (civisibility.TestSuite, bool) {
	sId := uint64(suite_id)
	if sId == 0 {
		return nil, false
	}
	exports.suiteMutex.RLock()
	defer exports.suiteMutex.RUnlock()
	suite, ok := exports.suites[sId]
	return suite, ok
}

// topt_suite_create creates or retrieves a test suite inside an existing module.
//
// Parameters:
//   - module_id: The parent module's ID.
//   - name: The name of the suite.
//   - start_time: Optional pointer to a topt_UnixTime for the suite start time.
//
// Returns:
//   - topt_SuiteResult: Contains the suite_id and a validity flag.
//
// Use topt_suite_close to close the suite when completed.
//
//export topt_suite_create
func topt_suite_create(module_id C.topt_ModuleId, name *C.char, start_time *C.topt_UnixTime) C.topt_SuiteResult {
	if name == nil {
		return C.topt_SuiteResult{suite_id: C.topt_SuiteId(0), valid: toBool(false)}
	}

	if module, ok := getModule(module_id); ok {
		var suiteOptions []civisibility.TestSuiteStartOption
		if start_time != nil {
			suiteOptions = append(suiteOptions, civisibility.WithTestSuiteStartTime(getUnixTime(start_time)))
		}

		suite := module.GetOrCreateSuite(C.GoString(name), suiteOptions...)
		id := suite.SuiteID()

		exports.suiteMutex.Lock()
		defer exports.suiteMutex.Unlock()
		exports.suites[id] = suite
		return C.topt_SuiteResult{suite_id: C.topt_SuiteId(id), valid: toBool(true)}
	}

	return C.topt_SuiteResult{suite_id: C.topt_SuiteId(0), valid: toBool(false)}
}

// topt_suite_close closes a test suite.
//
// Parameters:
//   - suite_id: The suite's ID.
//   - finish_time: Optional pointer to a topt_UnixTime for the suite end time.
//
// Returns:
//   - C.Bool: True if the suite was successfully closed, false otherwise.
//
//export topt_suite_close
func topt_suite_close(suite_id C.topt_SuiteId, finish_time *C.topt_UnixTime) C.Bool {
	if suite, ok := getSuite(suite_id); ok {
		if finish_time != nil {
			suite.Close(civisibility.WithTestSuiteFinishTime(getUnixTime(finish_time)))
		} else {
			suite.Close()
		}

		exports.suiteMutex.Lock()
		defer exports.suiteMutex.Unlock()
		delete(exports.suites, uint64(suite_id))
		return toBool(true)
	}

	return toBool(false)
}

// topt_suite_set_string_tag adds a string tag to a test suite.
//
// Parameters:
//   - suite_id: The suite's ID.
//   - key: Tag key string.
//   - value: Tag value string.
//
// Returns:
//   - C.Bool: True if successful, false otherwise.
//
//export topt_suite_set_string_tag
func topt_suite_set_string_tag(suite_id C.topt_SuiteId, key *C.char, value *C.char) C.Bool {
	if key == nil {
		return toBool(false)
	}
	if suite, ok := getSuite(suite_id); ok {
		suite.SetTag(C.GoString(key), C.GoString(value))
		return toBool(true)
	}
	return toBool(false)
}

// topt_suite_set_number_tag adds a numeric tag to a test suite.
//
// Parameters:
//   - suite_id: The suite's ID.
//   - key: Tag key string.
//   - value: A double representing the numeric value.
//
// Returns:
//   - C.Bool: True if successful, false otherwise.
//
//export topt_suite_set_number_tag
func topt_suite_set_number_tag(suite_id C.topt_SuiteId, key *C.char, value C.double) C.Bool {
	if key == nil {
		return toBool(false)
	}
	if suite, ok := getSuite(suite_id); ok {
		suite.SetTag(C.GoString(key), float64(value))
		return toBool(true)
	}
	return toBool(false)
}

// topt_suite_set_error records an error for a test suite.
//
// Parameters:
//   - suite_id: The suite's ID.
//   - error_type: Type/category of the error.
//   - error_message: The error message.
//   - error_stacktrace: Detailed stacktrace or error context.
//
// Returns:
//   - C.Bool: True if the error was recorded successfully, false otherwise.
//
//export topt_suite_set_error
func topt_suite_set_error(suite_id C.topt_SuiteId, error_type *C.char, error_message *C.char, error_stacktrace *C.char) C.Bool {
	if suite, ok := getSuite(suite_id); ok {
		suite.SetError(civisibility.WithErrorInfo(C.GoString(error_type), C.GoString(error_message), C.GoString(error_stacktrace)))
		return toBool(true)
	}
	return toBool(false)
}

// topt_suite_set_source sets the source code reference for the suite.
//
// Parameters:
//   - suite_id: The suite's ID.
//   - file: The source file name associated with the suite.
//   - start_line: Pointer to an integer representing the start line number in the source file.
//   - end_line: Pointer to an integer representing the end line number in the source file.
//
// Returns:
//   - C.Bool: True if the information was recorded successfully, false otherwise.
//
// This helps link tests to their source locations for better traceability.
//
//export topt_suite_set_source
func topt_suite_set_source(suite_id C.topt_SuiteId, file *C.char, start_line *C.int, end_line *C.int) C.Bool {
	if suite, ok := getSuite(suite_id); ok {
		if file != nil {
			gFile := C.GoString(file)
			gFile = utils.GetRelativePathFromCITagsSourceRoot(gFile)
			suite.SetTag(constants.TestSourceFile, gFile)

			// get the codeowner of the function
			codeOwners := utils.GetCodeOwners()
			if codeOwners != nil {
				match, found := codeOwners.Match("/" + gFile)
				if found {
					suite.SetTag(constants.TestCodeOwners, match.GetOwnersString())
				}
			}
		}
		if start_line != nil {
			suite.SetTag(constants.TestSourceStartLine, int(*start_line))
		}
		if end_line != nil {
			suite.SetTag(constants.TestSourceEndLine, int(*end_line))
		}
		return toBool(true)
	}
	return toBool(false)
}

// *******************************************************************************************************************
// Tests
// *******************************************************************************************************************

func getTest(test_id C.topt_TestId) (civisibility.Test, bool) {
	tId := uint64(test_id)
	if tId == 0 {
		return nil, false
	}
	exports.testMutex.RLock()
	defer exports.testMutex.RUnlock()
	test, ok := exports.tests[tId]
	return test, ok
}

// topt_test_create creates a new test within a suite.
//
// Parameters:
//   - suite_id: The parent suite's ID.
//   - name: The test name.
//   - start_time: Optional pointer to a topt_UnixTime for the test start time.
//
// Returns:
//   - topt_TestResult: Contains the test_id and a validity flag.
//
// Close the test later with topt_test_close once it completes.
//
//export topt_test_create
func topt_test_create(suite_id C.topt_SuiteId, name *C.char, start_time *C.topt_UnixTime) C.topt_TestResult {
	if name == nil {
		return C.topt_TestResult{test_id: C.topt_TestId(0), valid: toBool(false)}
	}

	if suite, ok := getSuite(suite_id); ok {
		var testOptions []civisibility.TestStartOption
		if start_time != nil {
			testOptions = append(testOptions, civisibility.WithTestStartTime(getUnixTime(start_time)))
		}

		test := suite.CreateTest(C.GoString(name), testOptions...)
		id := test.TestID()

		exports.testMutex.Lock()
		defer exports.testMutex.Unlock()
		exports.tests[id] = test
		return C.topt_TestResult{test_id: C.topt_TestId(id), valid: toBool(true)}
	}

	return C.topt_TestResult{test_id: C.topt_TestId(0), valid: toBool(false)}
}

// topt_test_close completes a test.
//
// Parameters:
//   - test_id: The ID of the test to close.
//   - options: A topt_TestCloseOptions struct specifying the test status, optional finish time, and skip reason.
//
// Returns:
//   - C.Bool: True if the test was found and successfully closed, false otherwise.
//
//export topt_test_close
func topt_test_close(test_id C.topt_TestId, options C.topt_TestCloseOptions) C.Bool {
	if test, ok := getTest(test_id); ok {
		var testOptions []civisibility.TestCloseOption
		if options.skip_reason != nil {
			testOptions = append(testOptions, civisibility.WithTestSkipReason(C.GoString(options.skip_reason)))
		}
		if options.finish_time != nil {
			testOptions = append(testOptions, civisibility.WithTestFinishTime(getUnixTime(options.finish_time)))
		}
		test.Close(civisibility.TestResultStatus(options.status), testOptions...)

		exports.testMutex.Lock()
		defer exports.testMutex.Unlock()
		delete(exports.tests, uint64(test_id))
		return toBool(true)
	}

	return toBool(false)
}

// topt_test_set_string_tag adds a string tag to a test.
//
// Parameters:
//   - test_id: The test's ID.
//   - key: The tag key string.
//   - value: The tag value string.
//
// Returns:
//   - C.Bool: True if successful, false otherwise.
//
//export topt_test_set_string_tag
func topt_test_set_string_tag(test_id C.topt_TestId, key *C.char, value *C.char) C.Bool {
	if key == nil {
		return toBool(false)
	}
	if test, ok := getTest(test_id); ok {
		test.SetTag(C.GoString(key), C.GoString(value))
		return toBool(true)
	}
	return toBool(false)
}

// topt_test_set_number_tag adds a numeric tag to a test.
//
// Parameters:
//   - test_id: The test's ID.
//   - key: The tag key string.
//   - value: A double representing the numeric value.
//
// Returns:
//   - C.Bool: True if successful, false otherwise.
//
//export topt_test_set_number_tag
func topt_test_set_number_tag(test_id C.topt_TestId, key *C.char, value C.double) C.Bool {
	if key == nil {
		return toBool(false)
	}
	if test, ok := getTest(test_id); ok {
		test.SetTag(C.GoString(key), float64(value))
		return toBool(true)
	}
	return toBool(false)
}

// topt_test_set_error marks a test as having encountered an error.
//
// Parameters:
//   - test_id: The test's ID.
//   - error_type: The type of the error.
//   - error_message: A descriptive error message.
//   - error_stacktrace: A stacktrace or detailed error context.
//
// Returns:
//   - C.Bool: True if the error was recorded successfully, false otherwise.
//
//export topt_test_set_error
func topt_test_set_error(test_id C.topt_TestId, error_type *C.char, error_message *C.char, error_stacktrace *C.char) C.Bool {
	if test, ok := getTest(test_id); ok {
		test.SetError(civisibility.WithErrorInfo(C.GoString(error_type), C.GoString(error_message), C.GoString(error_stacktrace)))
		return toBool(true)
	}
	return toBool(false)
}

// topt_test_set_source sets the source location for the test.
//
// Parameters:
//   - test_id: The test's ID.
//   - file: The source file name where the test is defined.
//   - start_line: Pointer to an integer indicating the start line of the test definition.
//   - end_line: Pointer to an integer indicating the end line of the test definition.
//
// Returns:
//   - C.Bool: True if successful, false otherwise.
//
//export topt_test_set_source
func topt_test_set_source(test_id C.topt_TestId, file *C.char, start_line *C.int, end_line *C.int) C.Bool {
	if test, ok := getTest(test_id); ok {
		if file != nil {
			gFile := C.GoString(file)
			gFile = utils.GetRelativePathFromCITagsSourceRoot(gFile)
			test.SetTag(constants.TestSourceFile, gFile)

			// get the codeowner of the function
			codeOwners := utils.GetCodeOwners()
			if codeOwners != nil {
				match, found := codeOwners.Match("/" + gFile)
				if found {
					test.SetTag(constants.TestCodeOwners, match.GetOwnersString())
				}
			}
		}
		if start_line != nil {
			test.SetTag(constants.TestSourceStartLine, int(*start_line))
		}
		if end_line != nil {
			test.SetTag(constants.TestSourceEndLine, int(*end_line))
		}
		return toBool(true)
	}
	return toBool(false)
}

// topt_test_set_benchmark_string_data sets benchmark-related data on a test using string key-value pairs.
//
// Parameters:
//   - test_id: The test's ID.
//   - measure_type: A string to categorize the benchmark data.
//   - data_array: A topt_KeyValueArray containing string key-value pairs.
//
// Returns:
//   - C.Bool: True if data was recorded successfully, false otherwise.
//
// Use this to attach performance metrics or custom benchmarking info in a string form.
//
//export topt_test_set_benchmark_string_data
func topt_test_set_benchmark_string_data(test_id C.topt_TestId, measure_type *C.char, data_array C.topt_KeyValueArray) C.Bool {
	if measure_type == nil {
		return toBool(false)
	}

	if test, ok := getTest(test_id); ok {
		data := make(map[string]any)
		for i := C.size_t(0); i < data_array.len; i++ {
			keyValue := *(*C.topt_KeyValuePair)(unsafe.Add(unsafe.Pointer(data_array.data), i*topt_KeyValuePair_Size))
			if keyValue.key == nil {
				continue
			}
			data[C.GoString(keyValue.key)] = C.GoString(keyValue.value)
		}

		test.SetBenchmarkData(C.GoString(measure_type), data)
		return toBool(true)
	}
	return toBool(false)
}

// topt_test_set_benchmark_number_data sets benchmark-related data on a test using numeric key-value pairs.
//
// Parameters:
//   - test_id: The test's ID.
//   - measure_type: A string describing the type of measurement.
//   - data_array: A topt_KeyNumberArray containing numeric key-value pairs.
//
// Returns:
//   - C.Bool: True if data was recorded successfully, false otherwise.
//
// Use this to record numerical performance metrics or custom stats for a test.
//
//export topt_test_set_benchmark_number_data
func topt_test_set_benchmark_number_data(test_id C.topt_TestId, measure_type *C.char, data_array C.topt_KeyNumberArray) C.Bool {
	if measure_type == nil {
		return toBool(false)
	}

	if test, ok := getTest(test_id); ok {
		data := make(map[string]any)
		for i := C.size_t(0); i < data_array.len; i++ {
			keyValue := *(*C.topt_KeyNumberPair)(unsafe.Add(unsafe.Pointer(data_array.data), i*topt_KeyNumberPair_Size))
			if keyValue.key == nil {
				continue
			}
			data[C.GoString(keyValue.key)] = float64(keyValue.value)
		}

		test.SetBenchmarkData(C.GoString(measure_type), data)
		return toBool(true)
	}
	return toBool(false)
}

// *******************************************************************************************************************
// Spans
// *******************************************************************************************************************

func getSpan(span_id C.topt_TslvId) (spanContainer, bool) {
	sId := uint64(span_id)
	if sId == 0 {
		return spanContainer{}, false
	}
	exports.spanMutex.RLock()
	defer exports.spanMutex.RUnlock()
	span, ok := exports.spans[sId]
	return span, ok
}

func getContext(tslv_id C.topt_TslvId) context.Context {
	if sContainer, ok := getSpan(C.topt_TestId(tslv_id)); ok {
		return sContainer.ctx
	}
	if test, ok := getTest(C.topt_TestId(tslv_id)); ok {
		return test.Context()
	}
	if suite, ok := getSuite(C.topt_SuiteId(tslv_id)); ok {
		return suite.Context()
	}
	if module, ok := getModule(C.topt_ModuleId(tslv_id)); ok {
		return module.Context()
	}
	if session, ok := getSession(C.topt_SessionId(tslv_id)); ok {
		return session.Context()
	}
	return context.Background()
}

// topt_span_create creates a new generic span as a child of a session, module, suite, test, or another span.
//
// Parameters:
//   - parent_id: The ID of the parent entity (session, module, suite, test, or span).
//   - span_options: A topt_SpanStartOptions struct containing span metadata, such as operation name, service name, resource name, start time, and tags.
//
// Returns:
//   - topt_SpanResult: Contains the new span_id and a validity flag.
//
// Spans represent trace segments and should be closed with topt_span_close.
//
//export topt_span_create
func topt_span_create(parent_id C.topt_TslvId, span_options C.topt_SpanStartOptions) C.topt_SpanResult {
	if span_options.operation_name == nil {
		return C.topt_SpanResult{span_id: C.topt_TslvId(0), valid: toBool(false)}
	}

	var options []ddtracer.StartSpanOption
	if span_options.start_time != nil {
		options = append(options, ddtracer.StartTime(getUnixTime(span_options.start_time)))
	}
	if span_options.service_name != nil {
		options = append(options, ddtracer.ServiceName(C.GoString(span_options.service_name)))
	}
	if span_options.resource_name != nil {
		options = append(options, ddtracer.ResourceName(C.GoString(span_options.resource_name)))
	}
	if span_options.span_type != nil {
		options = append(options, ddtracer.SpanType(C.GoString(span_options.span_type)))
	}
	if span_options.string_tags != nil {
		for i := C.size_t(0); i < span_options.string_tags.len; i++ {
			keyValue := (*C.topt_KeyValuePair)(unsafe.Add(unsafe.Pointer(span_options.string_tags.data), i*topt_KeyValuePair_Size))
			if keyValue.key == nil {
				continue
			}
			options = append(options, ddtracer.Tag(C.GoString(keyValue.key), C.GoString(keyValue.value)))
		}
	}
	if span_options.number_tags != nil {
		for i := C.size_t(0); i < span_options.number_tags.len; i++ {
			keyValue := (*C.topt_KeyNumberPair)(unsafe.Add(unsafe.Pointer(span_options.number_tags.data), i*topt_KeyNumberPair_Size))
			if keyValue.key == nil {
				continue
			}
			options = append(options, ddtracer.Tag(C.GoString(keyValue.key), float64(keyValue.value)))
		}
	}

	span, ctx := ddtracer.StartSpanFromContext(getContext(parent_id), C.GoString(span_options.operation_name), options...)
	id := span.Context().SpanID()

	exports.spanMutex.Lock()
	defer exports.spanMutex.Unlock()
	exports.spans[id] = spanContainer{span: span, ctx: ctx}
	return C.topt_SpanResult{span_id: C.topt_TslvId(id), valid: toBool(true)}
}

// topt_span_close finishes a previously opened span.
//
// Parameters:
//   - span_id: The ID of the span.
//   - finish_time: Optional pointer to a topt_UnixTime representing when the span ended.
//
// Returns:
//   - C.Bool: True if the span was found and closed successfully, false otherwise.
//
//export topt_span_close
func topt_span_close(span_id C.topt_TslvId, finish_time *C.topt_UnixTime) C.Bool {
	if sContainer, ok := getSpan(span_id); ok {
		if finish_time != nil {
			sContainer.span.Finish(ddtracer.FinishTime(getUnixTime(finish_time)))
		} else {
			sContainer.span.Finish()
		}

		exports.spanMutex.Lock()
		defer exports.spanMutex.Unlock()
		delete(exports.spans, uint64(span_id))
		return toBool(true)
	}

	return toBool(false)
}

// topt_span_set_string_tag adds a string tag to a span.
//
// Parameters:
//   - span_id: The span's ID.
//   - key: Tag key string.
//   - value: Tag value string.
//
// Returns:
//   - C.Bool: True if successful, false otherwise.
//
//export topt_span_set_string_tag
func topt_span_set_string_tag(span_id C.topt_TslvId, key *C.char, value *C.char) C.Bool {
	if key == nil {
		return toBool(false)
	}
	if sContainer, ok := getSpan(span_id); ok {
		sContainer.span.SetTag(C.GoString(key), C.GoString(value))
		return toBool(true)
	}
	return toBool(false)
}

// topt_span_set_number_tag adds a numeric tag to a span.
//
// Parameters:
//   - span_id: The span's ID.
//   - key: Tag key string.
//   - value: A double representing the numeric value.
//
// Returns:
//   - C.Bool: True if successful, false otherwise.
//
//export topt_span_set_number_tag
func topt_span_set_number_tag(span_id C.topt_TslvId, key *C.char, value C.double) C.Bool {
	if key == nil {
		return toBool(false)
	}
	if sContainer, ok := getSpan(span_id); ok {
		sContainer.span.SetTag(C.GoString(key), float64(value))
		return toBool(true)
	}
	return toBool(false)
}

// topt_span_set_error marks the span as having encountered an error.
//
// Parameters:
//   - span_id: The span's ID.
//   - error_type: A string describing the error type.
//   - error_message: A string describing the error message.
//   - error_stacktrace: A string containing a stacktrace or additional error details.
//
// Returns:
//   - C.Bool: True if the error was recorded, false otherwise.
//
//export topt_span_set_error
func topt_span_set_error(span_id C.topt_TslvId, error_type *C.char, error_message *C.char, error_stacktrace *C.char) C.Bool {
	if sContainer, ok := getSpan(span_id); ok {
		sContainer.span.SetTag(ext.Error, true)
		if error_type != nil {
			sContainer.span.SetTag(ext.ErrorType, C.GoString(error_type))
		}
		if error_message != nil {
			sContainer.span.SetTag(ext.ErrorMsg, C.GoString(error_message))
		}
		if error_stacktrace != nil {
			sContainer.span.SetTag(ext.ErrorStack, C.GoString(error_stacktrace))
		}
		return toBool(true)
	}
	return toBool(false)
}

// *******************************************************************************************************************
// Debugging
// *******************************************************************************************************************

// topt_debug_mock_tracer_reset resets the internal mock tracer instance used for testing scenarios.
//
// This function is designed for use in testing and debugging environments where a mock tracer
// has been previously initialized (via `topt_initialize` with `use_mock_tracer = true`) and
// test instrumentation and traces need to be cleared and reset. Calling this function will
// clear all previously recorded spans and state in the mock tracer, effectively returning it
// to a fresh, uninitialized state.
//
// Returns:
//   - Bool: Returns true if the mock tracer was successfully reset, or false if no mock tracer
//     is currently available.
//
// Usage notes:
//   - This function is intended for environments where deterministic or repeated tests occur
//     and the test harness requires a clean slate of tracing data between tests.
//   - Resetting the mock tracer does not affect real tracing operations in non-mock scenarios.
//   - If the library is not configured to use the mock tracer, this function will return false.
//
// Example usage:
//
//	if topt_debug_mock_tracer_reset() == 1 {
//	    // Mock tracer has been cleared and is ready for fresh test instrumentation.
//	} else {
//	    // No mock tracer was found, nothing to reset.
//	}
//
//export topt_debug_mock_tracer_reset
func topt_debug_mock_tracer_reset() C.Bool {
	if exports.mockTracer != nil {
		exports.mockTracer.Reset()
		return toBool(true)
	}
	return toBool(false)
}

func getMockSpanArrayFromSpanSlice(spans []mocktracer.Span) C.topt_MockSpanArray {
	spansCount := len(spans)
	if spansCount == 0 {
		return C.topt_MockSpanArray{len: C.size_t(0)}
	}

	spansSlice := make([]C.topt_MockSpan, spansCount)
	for i, span := range spans {
		mSpan := C.topt_MockSpan{
			span_id:        C.topt_TslvId(span.SpanID()),
			trace_id:       C.topt_TslvId(span.TraceID()),
			parent_span_id: C.topt_TslvId(span.ParentID()),
			start_time:     toUnixTime(span.StartTime()),
			finish_time:    toUnixTime(span.FinishTime()),
			operation_name: C.CString(span.OperationName()),
		}

		var stringTagsSlice []C.topt_KeyValuePair
		var numberTagsSlice []C.topt_KeyNumberPair
		for key, value := range span.Tags() {
			if strVal, ok := value.(string); ok {
				stringTagsSlice = append(stringTagsSlice, C.topt_KeyValuePair{
					key:   C.CString(key),
					value: C.CString(strVal),
				})
			}
			if numVal, ok := value.(float64); ok {
				numberTagsSlice = append(numberTagsSlice, C.topt_KeyNumberPair{
					key:   C.CString(key),
					value: C.double(numVal),
				})
			}
		}

		stringTagsData := unsafe.Pointer(C.malloc(C.size_t(len(stringTagsSlice)) * topt_KeyValuePair_Size))
		for i, tag := range stringTagsSlice {
			*(*C.topt_KeyValuePair)(unsafe.Add(stringTagsData, C.size_t(i)*topt_KeyValuePair_Size)) = tag
		}
		mSpan.string_tags = C.topt_KeyValueArray{
			data: (*C.topt_KeyValuePair)(stringTagsData),
			len:  C.size_t(len(stringTagsSlice)),
		}

		numberTagsData := unsafe.Pointer(C.malloc(C.size_t(len(numberTagsSlice)) * topt_KeyNumberPair_Size))
		for i, tag := range numberTagsSlice {
			*(*C.topt_KeyNumberPair)(unsafe.Add(numberTagsData, C.size_t(i)*topt_KeyNumberPair_Size)) = tag
		}
		mSpan.number_tags = C.topt_KeyNumberArray{
			data: (*C.topt_KeyNumberPair)(numberTagsData),
			len:  C.size_t(len(numberTagsSlice)),
		}

		spansSlice[i] = mSpan
	}

	cSpans := unsafe.Pointer(C.malloc(C.size_t(spansCount) * topt_MockSpan_Size))
	for i, mSpan := range spansSlice {
		*(*C.topt_MockSpan)(unsafe.Add(cSpans, C.size_t(i)*topt_MockSpan_Size)) = mSpan
	}

	return C.topt_MockSpanArray{
		data: (*C.topt_MockSpan)(cSpans),
		len:  C.size_t(spansCount),
	}
}

// topt_debug_mock_tracer_get_finished_spans retrieves all spans that have been finished in the mock tracer.
//
// This function returns a dynamically allocated array of `topt_MockSpan` structs, each representing a
// completed span recorded by the currently active mock tracer (if any).
//
// Returns:
//   - `topt_MockSpanArray`: A struct containing a pointer to an array of `topt_MockSpan` and its length. If
//     the mock tracer is not in use or no spans have been finished, the returned `len` will be zero.
//
// Usage notes:
//   - This function is only meaningful if `topt_initialize` was called with `use_mock_tracer = true`; otherwise
//     it will return an empty array.
//   - The memory allocated for the returned array (and its internal C strings) must be freed using
//     `topt_debug_mock_tracer_free_mock_span_array` to avoid memory leaks.
//
// Example usage:
//
//	// Call this after tests or instrumentation are complete to inspect finished spans.
//	finishedSpans := topt_debug_mock_tracer_get_finished_spans()
//	if finishedSpans.len > 0 {
//	    // Process the finished spans...
//	}
//	topt_debug_mock_tracer_free_mock_span_array(finishedSpans)
//
//export topt_debug_mock_tracer_get_finished_spans
func topt_debug_mock_tracer_get_finished_spans() C.topt_MockSpanArray {
	if exports.mockTracer == nil {
		return C.topt_MockSpanArray{len: C.size_t(0)}
	}

	spans := exports.mockTracer.FinishedSpans()
	return getMockSpanArrayFromSpanSlice(spans)
}

// topt_debug_mock_tracer_get_open_spans retrieves all spans that are currently
// in-progress (open) in the mock tracer.
//
// Returns:
//   - topt_MockSpanArray: A struct containing a pointer to an array of topt_MockSpan
//     and its length. If the mock tracer is not in use, or if there are no open spans,
//     the returned array will have len = 0.
//
// Usage notes:
//   - This function is only meaningful if topt_initialize was called with
//     use_mock_tracer = true; otherwise, it will return an empty array.
//   - The memory allocated for the returned array (and all C strings within each
//     topt_MockSpan) must be freed using topt_debug_mock_tracer_free_mock_span_array
//     to avoid memory leaks.
//
// Example usage:
//
//	topt_MockSpanArray openSpans = topt_debug_mock_tracer_get_open_spans();
//	if (openSpans.len > 0) {
//	    // Inspect or log the in-progress spans here...
//	}
//	topt_debug_mock_tracer_free_mock_span_array(openSpans);
//
//export topt_debug_mock_tracer_get_open_spans
func topt_debug_mock_tracer_get_open_spans() C.topt_MockSpanArray {
	if exports.mockTracer == nil {
		return C.topt_MockSpanArray{len: C.size_t(0)}
	}

	spans := exports.mockTracer.OpenSpans()
	return getMockSpanArrayFromSpanSlice(spans)
}

// topt_debug_mock_tracer_free_mock_span_array deallocates all memory previously
// allocated and returned by the mock tracer retrieval functions (e.g.,
// topt_debug_mock_tracer_get_finished_spans or topt_debug_mock_tracer_get_open_spans).
//
// Parameters:
//   - spans: A topt_MockSpanArray structure obtained from
//     topt_debug_mock_tracer_get_finished_spans or topt_debug_mock_tracer_get_open_spans.
//
// What this function frees:
//   - The top-level array of topt_MockSpan itself.
//   - Each operation_name string (if not NULL).
//   - All strings (both key and value) inside each topt_KeyValuePair in string_tags.
//   - All key strings inside each topt_KeyNumberPair in number_tags.
//
// Usage notes:
//   - You must call this function once you have finished inspecting or using
//     the spans data, to avoid memory leaks.
//   - If spans.data is NULL or spans.len is zero, this function does nothing.
//
// Example usage:
//
//	topt_MockSpanArray finishedSpans = topt_debug_mock_tracer_get_finished_spans();
//	if (finishedSpans.len > 0) {
//	    // Process or log the finished spans here...
//	}
//	topt_debug_mock_tracer_free_mock_span_array(finishedSpans);
//
//export topt_debug_mock_tracer_free_mock_span_array
func topt_debug_mock_tracer_free_mock_span_array(spans C.topt_MockSpanArray) {
	if spans.data != nil {
		for i := C.size_t(0); i < spans.len; i++ {
			mSpan := *(*C.topt_MockSpan)(unsafe.Add(unsafe.Pointer(spans.data), i*topt_MockSpan_Size))
			if mSpan.operation_name != nil {
				C.free(unsafe.Pointer(mSpan.operation_name))
			}
			if mSpan.string_tags.data != nil {
				for j := C.size_t(0); j < mSpan.string_tags.len; j++ {
					tag := *(*C.topt_KeyValuePair)(unsafe.Add(unsafe.Pointer(mSpan.string_tags.data), j*topt_KeyValuePair_Size))
					if tag.key != nil {
						C.free(unsafe.Pointer(tag.key))
					}
					if tag.value != nil {
						C.free(unsafe.Pointer(tag.value))
					}
				}
				C.free(unsafe.Pointer(mSpan.string_tags.data))
			}
			if mSpan.number_tags.data != nil {
				for j := C.size_t(0); j < mSpan.number_tags.len; j++ {
					tag := *(*C.topt_KeyNumberPair)(unsafe.Add(unsafe.Pointer(mSpan.number_tags.data), j*topt_KeyNumberPair_Size))
					if tag.key != nil {
						C.free(unsafe.Pointer(tag.key))
					}
				}
				C.free(unsafe.Pointer(mSpan.number_tags.data))
			}
		}
		C.free(unsafe.Pointer(spans.data))
	}
}
