// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

//go:build civisibility_native
// +build civisibility_native

package main

// #cgo darwin CFLAGS: -mmacosx-version-min=11.0
// #cgo android CFLAGS: --sysroot=$NDK_ROOT/toolchains/llvm/prebuilt/darwin-x86_64/sysroot
// #cgo LDFLAGS: -s -w -llib
// #include <stdlib.h>
/*
typedef unsigned char Bool;
typedef unsigned long long Uint64;
typedef Uint64 topt_TslvId;
typedef topt_TslvId topt_SessionId;
typedef topt_TslvId topt_ModuleId;
typedef topt_TslvId topt_SuiteId;
typedef topt_TslvId topt_TestId;
typedef unsigned char topt_TestStatus;

// topt_TestStatusPass is the status for a passing test.
const topt_TestStatus topt_TestStatusPass = 0;
// topt_TestStatusFail is the status for a failing test.
const topt_TestStatus topt_TestStatusFail = 1;
// topt_TestStatusSkip is the status for a skipped test.
const topt_TestStatus topt_TestStatusSkip = 2;

// topt_SessionResult is used to return the result of a session creation.
typedef struct {
	topt_SessionId session_id;
	Bool valid;
} topt_SessionResult;

// topt_ModuleResult is used to return the result of a module creation.
typedef struct {
	topt_ModuleId module_id;
	Bool valid;
} topt_ModuleResult;

// topt_SuiteResult is used to return the result of a suite creation.
typedef struct {
	topt_SuiteId suite_id;
	Bool valid;
} topt_SuiteResult;

// topt_TestResult is used to return the result of a test creation.
typedef struct {
	topt_TestId test_id;
	Bool valid;
} topt_TestResult;

// topt_KeyValuePair is used to store a key-value pair.
typedef struct {
    char* key;
    char* value;
} topt_KeyValuePair;

// topt_KeyValueArray is used to store an array of key-value pairs.
typedef struct {
    topt_KeyValuePair* data;
    size_t len;
} topt_KeyValueArray;

// topt_KeyNumberPair is used to store a key-number pair.
typedef struct {
	char* key;
	double value;
} topt_KeyNumberPair;

// topt_KeyNumberArray is used to store an array of key-number pairs.
typedef struct {
	topt_KeyNumberPair* data;
	size_t len;
} topt_KeyNumberArray;

// topt_InitOptions is used to initialize the library.
typedef struct {
    char* language;
    char* runtime_name;
    char* runtime_version;
    char* working_directory;
    topt_KeyValueArray* environment_variables;
	topt_KeyValueArray* global_tags;
	// Unused fields
	void* unused01;
	void* unused02;
	void* unused03;
	void* unused04;
	void* unused05;
} topt_InitOptions;

// topt_UnixTime is used to store a Unix timestamp.
typedef struct {
    Uint64 sec;
    Uint64 nsec;
} topt_UnixTime;

// topt_TestCloseOptions is used to close a test with additional options.
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

// topt_SettingsEarlyFlakeDetectionSlowRetries is used to store the settings for slow retries in early flake detection.
typedef struct {
	int ten_s;
	int thirty_s;
	int five_m;
	int five_s;
} topt_SettingsEarlyFlakeDetectionSlowRetries;

// topt_SettingsEarlyFlakeDetection is used to store the settings for early flake detection.
typedef struct {
	Bool enabled;
	topt_SettingsEarlyFlakeDetectionSlowRetries slow_test_retries;
	int faulty_session_threshold;
} topt_SettingsEarlyFlakeDetection;

// topt_SettingsResponse is used to return the settings of the library.
typedef struct {
	Bool code_coverage;
	topt_SettingsEarlyFlakeDetection early_flake_detection;
	Bool flaky_test_retries_enabled;
	Bool itr_enabled;
	Bool require_git;
	Bool tests_skipping;
	// Unused fields
	void* unused01;
	void* unused02;
	void* unused03;
	void* unused04;
	void* unused05;
} topt_SettingsResponse;

// topt_FlakyTestRetriesSettings is used to store the settings for flaky test retries.
typedef struct {
	int retry_count;
	int total_retry_count;
} topt_FlakyTestRetriesSettings;

// topt_KnownTest is used to store a known test.
typedef struct {
	char* module_name;
	char* suite_name;
	char* test_name;
} topt_KnownTest;

// topt_KnownTestArray is used to store an array of known tests.
typedef struct {
	topt_KnownTest* data;
	size_t len;
} topt_KnownTestArray;

// topt_SkippableTest is used to store a skippable test.
typedef struct {
	char* suite_name;
	char* test_name;
	char* parameters;
	char* custom_configurations_json;
} topt_SkippableTest;

// topt_SkippableTestArray is used to store an array of skippable tests.
typedef struct {
	topt_SkippableTest* data;
	size_t len;
} topt_SkippableTestArray;

// topt_TestCoverageFile is used to store a test coverage file.
typedef struct {
	char* filename;
	void* bitmap;
	size_t bitmap_len;
} topt_TestCoverageFile;

// toptTestCoverage is used to store the test coverage data.
typedef struct {
	topt_SessionId session_id;
	topt_SuiteId suite_id;
	topt_TestId test_id;
	topt_TestCoverageFile* files;
	size_t files_len;
} topt_TestCoverage;

// topt_SpanStartOptions is used to store the options for starting a span.
typedef struct {
	char* operation_name;
	char* service_name;
	char* resource_name;
	char* span_type;
	topt_UnixTime* start_time;
    topt_KeyValueArray* string_tags;
	topt_KeyNumberArray* number_tags;
} topt_SpanStartOptions;

// topt_SpanResult is used to store the result of a span creation.
typedef struct {
	topt_TslvId span_id;
	Bool valid;
} topt_SpanResult;
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
	topt_KeyValuePair_Size     = C.size_t(unsafe.Sizeof(C.topt_KeyValuePair{}))
	topt_KeyNumberPair_Size    = C.size_t(unsafe.Sizeof(C.topt_KeyNumberPair{}))
	topt_KnownTest_Size        = C.size_t(unsafe.Sizeof(C.topt_KnownTest{}))
	topt_SkippableTest_Size    = C.size_t(unsafe.Sizeof(C.topt_SkippableTest{}))
	topt_TestCoverageFile_Size = C.size_t(unsafe.Sizeof(C.topt_TestCoverageFile{}))
	topt_TestCoverage_Size     = C.size_t(unsafe.Sizeof(C.topt_TestCoverage{}))
)

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

// toBool converts a Go bool to a C.Bool (0 or 1).
func toBool(value bool) C.Bool {
	if value {
		return C.Bool(1)
	}
	return C.Bool(0)
}

// *******************************************************************************************************************
// General
// *******************************************************************************************************************

var (
	hasInitialized atomic.Bool // indicate if the library has been initialized
	canShutdown    atomic.Bool // indicate if the library can be shut down

	client = net.NewClientForCodeCoverage() // client to send code coverage payloads
)

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

// topt_initialize initializes the library with the given options.
//
//export topt_initialize
func topt_initialize(options C.topt_InitOptions) C.Bool {
	if hasInitialized.Swap(true) {
		return toBool(false)
	}

	canShutdown.Store(true)
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
	civisibility.EnsureCiVisibilityInitialization()
	return toBool(true)
}

// topt_shutdown shuts down the library.
//
//export topt_shutdown
func topt_shutdown() C.Bool {
	if !canShutdown.Swap(false) {
		return toBool(false)
	}
	civisibility.ExitCiVisibility()
	return toBool(true)
}

// topt_get_settings returns the settings of the library.
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
	}
}

// topt_get_flaky_test_retries_settings returns the settings for flaky test retries.
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

// topt_get_known_tests returns the known tests.
//
//export topt_get_known_tests
func topt_get_known_tests() C.topt_KnownTestArray {
	var knownTests []C.topt_KnownTest
	for moduleName, module := range civisibility.GetEarlyFlakeDetectionSettings().Tests {
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

// topt_free_known_tests frees the known tests array
//
//export topt_free_known_tests
func topt_free_known_tests(knownTests C.topt_KnownTestArray) {
	for i := C.size_t(0); i < knownTests.len; i++ {
		knownTest := *(*C.topt_KnownTest)(unsafe.Add(unsafe.Pointer(knownTests.data), i*topt_KnownTest_Size))
		C.free(unsafe.Pointer(knownTest.module_name))
		C.free(unsafe.Pointer(knownTest.suite_name))
		C.free(unsafe.Pointer(knownTest.test_name))
	}
	C.free(unsafe.Pointer(knownTests.data))
}

// topt_get_skippable_tests returns the skippable tests.
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

// topt_free_skippable_tests frees the skippable tests array
//
//export topt_free_skippable_tests
func topt_free_skippable_tests(skippableTests C.topt_SkippableTestArray) {
	for i := C.size_t(0); i < skippableTests.len; i++ {
		skippableTest := *(*C.topt_SkippableTest)(unsafe.Add(unsafe.Pointer(skippableTests.data), i*topt_SkippableTest_Size))
		C.free(unsafe.Pointer(skippableTest.suite_name))
		C.free(unsafe.Pointer(skippableTest.test_name))
		C.free(unsafe.Pointer(skippableTest.parameters))
		C.free(unsafe.Pointer(skippableTest.custom_configurations_json))
	}
	C.free(unsafe.Pointer(skippableTests.data))
}

// topt_send_code_coverage_payload sends the code coverage payload.
//
//export topt_send_code_coverage_payload
func topt_send_code_coverage_payload(coverages *C.topt_TestCoverage, coverages_length C.size_t) {
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

	if coverages_length > 0 {
		// Create a new buffer to encode the coverage payload in MessagePack format
		encodedBuf := new(bytes.Buffer)
		jsonbytes, err := json.Marshal(&coveragePayload)
		if err == nil {
			encodedBuf.Write(jsonbytes)
			client.SendCoveragePayloadWithFormat(encodedBuf, net.FormatJSON)
		}
	}
}

// *******************************************************************************************************************
// Sessions
// *******************************************************************************************************************

var (
	sessionMutex sync.RWMutex                                // mutex to protect the session map
	sessions     = make(map[uint64]civisibility.TestSession) // map of test sessions
)

func getSession(session_id C.topt_SessionId) (civisibility.TestSession, bool) {
	sId := uint64(session_id)
	if sId == 0 {
		return nil, false
	}
	sessionMutex.RLock()
	defer sessionMutex.RUnlock()
	session, ok := sessions[sId]
	return session, ok
}

// topt_session_create creates a new test session.
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

	sessionMutex.Lock()
	defer sessionMutex.Unlock()
	sessions[id] = session
	return C.topt_SessionResult{session_id: C.topt_SessionId(id), valid: toBool(true)}
}

// topt_session_close closes the test session with the given ID.
//
//export topt_session_close
func topt_session_close(session_id C.topt_SessionId, exit_code C.int, finish_time *C.topt_UnixTime) C.Bool {
	if session, ok := getSession(session_id); ok {
		if finish_time != nil {
			session.Close(int(exit_code), civisibility.WithTestSessionFinishTime(getUnixTime(finish_time)))
		} else {
			session.Close(int(exit_code))
		}

		sessionMutex.Lock()
		defer sessionMutex.Unlock()
		delete(sessions, uint64(session_id))
		return toBool(true)
	}

	return toBool(false)
}

// topt_session_set_string_tag sets a string tag for the test session with the given ID.
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

// topt_session_set_number_tag sets a number tag for the test session with the given ID.
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

// topt_session_set_error sets an error for the test session with the given ID.
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

var (
	moduleMutex sync.RWMutex                               // mutex to protect the modules map
	modules     = make(map[uint64]civisibility.TestModule) // map of test modules
)

func getModule(module_id C.topt_ModuleId) (civisibility.TestModule, bool) {
	mId := uint64(module_id)
	if mId == 0 {
		return nil, false
	}
	moduleMutex.RLock()
	defer moduleMutex.RUnlock()
	module, ok := modules[mId]
	return module, ok
}

// topt_module_create creates a new test module.
//
//export topt_module_create
func topt_module_create(session_id C.topt_SessionId, name *C.char, framework *C.char, framework_version *C.char, start_time *C.topt_UnixTime) C.topt_ModuleResult {
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

		moduleMutex.Lock()
		defer moduleMutex.Unlock()
		modules[id] = module
		return C.topt_ModuleResult{module_id: C.topt_ModuleId(id), valid: toBool(true)}
	}

	return C.topt_ModuleResult{module_id: C.topt_ModuleId(0), valid: toBool(false)}
}

// topt_module_close closes the test module with the given ID.
//
//export topt_module_close
func topt_module_close(module_id C.topt_ModuleId, finish_time *C.topt_UnixTime) C.Bool {
	if module, ok := getModule(module_id); ok {
		if finish_time != nil {
			module.Close(civisibility.WithTestModuleFinishTime(getUnixTime(finish_time)))
		} else {
			module.Close()
		}

		moduleMutex.Lock()
		defer moduleMutex.Unlock()
		delete(modules, uint64(module_id))
		return toBool(true)
	}

	return toBool(false)
}

// topt_module_set_string_tag sets a string tag for the test module with the given ID.
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

// topt_module_set_number_tag sets a number tag for the test module with the given ID.
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

// topt_module_set_error sets an error for the test module with the given ID.
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

var (
	suiteMutex sync.RWMutex                              // mutex to protect the suites map
	suites     = make(map[uint64]civisibility.TestSuite) // map of test suites
)

func getSuite(suite_id C.topt_SuiteId) (civisibility.TestSuite, bool) {
	sId := uint64(suite_id)
	if sId == 0 {
		return nil, false
	}
	suiteMutex.RLock()
	defer suiteMutex.RUnlock()
	suite, ok := suites[sId]
	return suite, ok
}

// topt_suite_create creates a new test suite.
//
//export topt_suite_create
func topt_suite_create(module_id C.topt_ModuleId, name *C.char, start_time *C.topt_UnixTime) C.topt_SuiteResult {
	if module, ok := getModule(module_id); ok {
		var suiteOptions []civisibility.TestSuiteStartOption
		if start_time != nil {
			suiteOptions = append(suiteOptions, civisibility.WithTestSuiteStartTime(getUnixTime(start_time)))
		}

		suite := module.GetOrCreateSuite(C.GoString(name), suiteOptions...)
		id := suite.SuiteID()

		suiteMutex.Lock()
		defer suiteMutex.Unlock()
		suites[id] = suite
		return C.topt_SuiteResult{suite_id: C.topt_SuiteId(id), valid: toBool(true)}
	}

	return C.topt_SuiteResult{suite_id: C.topt_SuiteId(0), valid: toBool(false)}
}

// topt_suite_close closes the test suite with the given ID.
//
//export topt_suite_close
func topt_suite_close(suite_id C.topt_SuiteId, finish_time *C.topt_UnixTime) C.Bool {
	if suite, ok := getSuite(suite_id); ok {
		if finish_time != nil {
			suite.Close(civisibility.WithTestSuiteFinishTime(getUnixTime(finish_time)))
		} else {
			suite.Close()
		}

		suiteMutex.Lock()
		defer suiteMutex.Unlock()
		delete(suites, uint64(suite_id))
		return toBool(true)
	}

	return toBool(false)
}

// topt_suite_set_string_tag sets a string tag for the test suite with the given ID.
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

// topt_suite_set_number_tag sets a number tag for the test suite with the given ID.
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

// topt_suite_set_error sets an error for the test suite with the given ID.
//
//export topt_suite_set_error
func topt_suite_set_error(suite_id C.topt_SuiteId, error_type *C.char, error_message *C.char, error_stacktrace *C.char) C.Bool {
	if suite, ok := getSuite(suite_id); ok {
		suite.SetError(civisibility.WithErrorInfo(C.GoString(error_type), C.GoString(error_message), C.GoString(error_stacktrace)))
		return toBool(true)
	}
	return toBool(false)
}

// topt_suite_set_source sets the source file, start line, and end line for the test suite with the given ID.
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

var (
	testMutex sync.RWMutex                         // mutex to protect the tests map
	tests     = make(map[uint64]civisibility.Test) // map of test spans
)

func getTest(test_id C.topt_TestId) (civisibility.Test, bool) {
	tId := uint64(test_id)
	if tId == 0 {
		return nil, false
	}
	testMutex.RLock()
	defer testMutex.RUnlock()
	test, ok := tests[tId]
	return test, ok
}

// topt_test_create creates a new test span.
//
//export topt_test_create
func topt_test_create(suite_id C.topt_SuiteId, name *C.char, start_time *C.topt_UnixTime) C.topt_TestResult {
	if suite, ok := getSuite(suite_id); ok {
		var testOptions []civisibility.TestStartOption
		if start_time != nil {
			testOptions = append(testOptions, civisibility.WithTestStartTime(getUnixTime(start_time)))
		}

		test := suite.CreateTest(C.GoString(name), testOptions...)
		id := test.TestID()

		testMutex.Lock()
		defer testMutex.Unlock()
		tests[id] = test
		return C.topt_TestResult{test_id: C.topt_TestId(id), valid: toBool(true)}
	}

	return C.topt_TestResult{test_id: C.topt_TestId(0), valid: toBool(false)}
}

// topt_test_close closes the test span with the given ID.
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

		testMutex.Lock()
		defer testMutex.Unlock()
		delete(tests, uint64(test_id))
		return toBool(true)
	}

	return toBool(false)
}

// topt_test_set_string_tag sets a string tag for the test span with the given ID.
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

// topt_test_set_number_tag sets a number tag for the test span with the given ID.
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

// topt_test_set_error sets an error for the test span with the given ID.
//
//export topt_test_set_error
func topt_test_set_error(test_id C.topt_TestId, error_type *C.char, error_message *C.char, error_stacktrace *C.char) C.Bool {
	if test, ok := getTest(test_id); ok {
		test.SetError(civisibility.WithErrorInfo(C.GoString(error_type), C.GoString(error_message), C.GoString(error_stacktrace)))
		return toBool(true)
	}
	return toBool(false)
}

// topt_test_set_source sets the source file, start line, and end line for the test span with the given ID.
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

// topt_test_set_benchmark_data sets the benchmark data for the test span with the given ID.
//
//export topt_test_set_benchmark_string_data
func topt_test_set_benchmark_string_data(test_id C.topt_TestId, measure_type *C.char, data_array C.topt_KeyValueArray) C.Bool {
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

// topt_test_set_benchmark_number_data sets the benchmark number data for the test span with the given ID.
//
//export topt_test_set_benchmark_number_data
func topt_test_set_benchmark_number_data(test_id C.topt_TestId, measure_type *C.char, data_array C.topt_KeyNumberArray) C.Bool {
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

type (
	// spanContainer represents a span and its context.
	spanContainer struct {
		span ddtracer.Span
		ctx  context.Context
	}
)

var (
	spanMutex sync.RWMutex                     // mutex to protect the spans map
	spans     = make(map[uint64]spanContainer) // map of spans
)

func getSpan(span_id C.topt_TslvId) (spanContainer, bool) {
	sId := uint64(span_id)
	if sId == 0 {
		return spanContainer{}, false
	}
	spanMutex.RLock()
	defer spanMutex.RUnlock()
	span, ok := spans[sId]
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

// topt_span_create creates a new span.
//
//export topt_span_create
func topt_span_create(parent_id C.topt_TslvId, span_options C.topt_SpanStartOptions) C.topt_SpanResult {
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

	spanMutex.Lock()
	defer spanMutex.Unlock()
	spans[id] = spanContainer{span: span, ctx: ctx}
	return C.topt_SpanResult{span_id: C.topt_TslvId(id), valid: toBool(true)}
}

// topt_span_close closes the span with the given ID.
//
//export topt_span_close
func topt_span_close(span_id C.topt_TslvId, finish_time *C.topt_UnixTime) C.Bool {
	if sContainer, ok := getSpan(span_id); ok {
		if finish_time != nil {
			sContainer.span.Finish(ddtracer.FinishTime(getUnixTime(finish_time)))
		} else {
			sContainer.span.Finish()
		}

		spanMutex.Lock()
		defer spanMutex.Unlock()
		delete(spans, uint64(span_id))
		return toBool(true)
	}

	return toBool(false)
}

// topt_span_set_string_tag sets a string tag for the span with the given ID.
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

// topt_span_set_number_tag sets a number tag for the span with the given ID.
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

// topt_span_set_error sets an error for the span with the given ID.
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
