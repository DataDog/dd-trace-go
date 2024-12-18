// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

//go:build civisibility_native
// +build civisibility_native

package main

// #cgo darwin CFLAGS: -mmacosx-version-min=11.0
// #cgo android CFLAGS: --sysroot=$NDK_ROOT/toolchains/llvm/prebuilt/darwin-x86_64/sysroot
// #cgo LDFLAGS: -s -w
/*
typedef unsigned char Bool;
typedef unsigned long long Uint64;
typedef Uint64 topt_SessionId;
typedef Uint64 topt_ModuleId;
typedef Uint64 topt_SuiteId;
typedef Uint64 topt_TestId;
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
const int topt_SessionResult_Size = sizeof(topt_SessionResult);

// topt_ModuleResult is used to return the result of a module creation.
typedef struct {
	topt_ModuleId module_id;
	Bool valid;
} topt_ModuleResult;
const int topt_ModuleResult_Size = sizeof(topt_ModuleResult);

// topt_SuiteResult is used to return the result of a suite creation.
typedef struct {
	topt_SuiteId suite_id;
	Bool valid;
} topt_SuiteResult;
const int topt_SuiteResult_Size = sizeof(topt_SuiteResult);

// topt_TestResult is used to return the result of a test creation.
typedef struct {
	topt_TestId test_id;
	Bool valid;
} topt_TestResult;
const int topt_TestResult_Size = sizeof(topt_TestResult);

// topt_KeyValuePair is used to store a key-value pair.
typedef struct {
    char* key;
    char* value;
} topt_KeyValuePair;
const int topt_KeyValuePair_Size = sizeof(topt_KeyValuePair_Size);

// topt_KeyValueArray is used to store an array of key-value pairs.
typedef struct {
    topt_KeyValuePair* data;
    Uint64 len;
} topt_KeyValueArray;
const int topt_KeyValueArray_Size = sizeof(topt_KeyValueArray);

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
const int topt_InitOptions_Size = sizeof(topt_InitOptions);

// topt_UnixTime is used to store a Unix timestamp.
typedef struct {
    Uint64 sec;
    Uint64 nsec;
} topt_UnixTime;
const int topt_UnixTime_Size = sizeof(topt_UnixTime);

// topt_TestCloseOptions is used to close a test with additional options.
typedef struct {
	topt_TestStatus status;
	topt_UnixTime* finish_time;
	char* skip_reason;
} topt_TestCloseOptions;
const int topt_TestCloseOptions_Size = sizeof(topt_TestCloseOptions);

typedef struct {
	int ten_s;
	int thirty_s;
	int five_m;
	int five_s;
} topt_SettingsEarlyFlakeDetectionSlowRetries;
const int topt_SettingsEarlyFlakeDetectionSlowRetries_Size = sizeof(topt_SettingsEarlyFlakeDetectionSlowRetries);

typedef struct {
	Bool enabled;
	topt_SettingsEarlyFlakeDetectionSlowRetries slow_test_retries;
	int faulty_session_threshold;
} topt_SettingsEarlyFlakeDetection;
const int topt_SettingsEarlyFlakeDetection_Size = sizeof(topt_SettingsEarlyFlakeDetection);

typedef struct {
	Bool code_coverage;
	topt_SettingsEarlyFlakeDetection early_flake_detection;
	Bool flaky_test_retries_enabled;
	Bool itr_enabled;
	Bool require_git;
	Bool tests_skipping;
} topt_SettingsResponse;
const int topt_SettingsResponse_Size = sizeof(topt_SettingsResponse);

typedef struct {
	int retry_count;
	int total_retry_count;
} topt_FlakyTestRetriesSettings;
const int topt_FlakyTestRetriesSettings_Size = sizeof(topt_FlakyTestRetriesSettings);

typedef struct {
	char* module_name;
	char* suite_name;
	char* test_name;
} topt_KnownTest;
const int topt_KnownTest_Size = sizeof(topt_KnownTest);

typedef struct {
	char* suite_name;
	char* test_name;
	char* parameters;
	char* custom_configurations_json;
} topt_SkippableTest;
const int topt_SkippableTest_Size = sizeof(topt_SkippableTest);

typedef struct {
	char* filename;
	char* bitmap;
} topt_TestCoverageFile;
const int topt_TestCoverageFile_Size = sizeof(topt_TestCoverageFile);

typedef struct {
	topt_SuiteId test_suite_id;
	topt_TestId span_id;
	topt_TestCoverageFile* files;
	Uint64 files_len;
} topt_TestCoverage;
const int topt_TestCoverage_Size = sizeof(topt_TestCoverage);
*/
import "C"
import (
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	civisibility "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
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
)

// topt_initialize initializes the library with the given options.
//
//export topt_initialize
func topt_initialize(options C.topt_InitOptions) C.Bool {
	if hasInitialized.Swap(true) {
		return toBool(false)
	}

	canShutdown.Store(true)
	var tags map[string]string
	if options.environment_variables != nil {
		sLen := int(options.environment_variables.len)
		kvSize := int(C.topt_KeyValuePair_Size)
		for i := 0; i < sLen; i++ {
			keyValue := (*C.topt_KeyValuePair)(unsafe.Add(unsafe.Pointer(options.environment_variables.data), i*kvSize))
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
		sLen := int(options.global_tags.len)
		kvSize := int(C.topt_KeyValuePair_Size)
		for i := 0; i < sLen; i++ {
			keyValue := (*C.topt_KeyValuePair)(unsafe.Add(unsafe.Pointer(options.global_tags.data), i*kvSize))
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
