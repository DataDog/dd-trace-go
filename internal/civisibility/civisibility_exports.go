// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build civisibility
// +build civisibility

// CGO_CFLAGS=-mmacosx-version-min=11.0 go build -tags civisibility -buildmode=c-archive -ldflags "-s -w" -o libcivisibility.a civisibility_exports.go
//
// CGO_CFLAGS=-mmacosx-version-min=11.0 GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -tags civisibility -buildmode=c-archive -ldflags "-s -w" -o libcivisibility.a civisibility_exports.go
// CGO_CFLAGS=-mmacosx-version-min=11.0 GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -tags civisibility -buildmode=c-archive -ldflags "-s -w" -o libcivisibility.a civisibility_exports.go
// GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build -tags civisibility -buildmode=c-archive -ldflags "-s -w" -o /tmp/lima/libcivisibility.a civisibility_exports.go
// GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-linux-gnu-gcc go build -tags civisibility -buildmode=c-archive -ldflags "-s -w" -o /tmp/lima/libcivisibility.a civisibility_exports.go
// GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build -tags civisibility -buildmode=c-archive -ldflags "-s -w" -o libcivisibility.lib civisibility_exports.go

package main

/*
struct unix_time {
    unsigned long long sec;
    unsigned long long nsec;
};
struct setting_early_flake_detection_slow_test_retries {
	int ten_s;
	int thirty_s;
	int five_m;
	int five_s;
};
struct setting_early_flake_detection {
	unsigned char enabled;
	struct setting_early_flake_detection_slow_test_retries slow_test_retries;
	int faulty_session_threshold;
};
struct settings_response {
	unsigned char code_coverage;
	struct setting_early_flake_detection early_flake_detection;
	unsigned char flaky_test_retries_enabled;
	unsigned char itr_enabled;
	unsigned char require_git;
	unsigned char tests_skipping;
};
struct flaky_test_retries_settings {
	int retry_count;
	int total_retry_count;
};
struct known_test {
	char* module_name;
	char* suite_name;
	char* test_name;
};
*/
import "C"
import (
	"sync"
	"time"
	"unsafe"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	civisibility "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
)

const known_test_size = 3 * int(unsafe.Sizeof(uintptr(0)))

var (
	session civisibility.TestSession

	modulesMutex sync.RWMutex
	modules      = make(map[uint64]civisibility.TestModule)

	suitesMutex sync.RWMutex
	suites      = make(map[uint64]civisibility.TestSuite)

	testsMutex sync.RWMutex
	tests      = make(map[uint64]civisibility.Test)
)

func getUnixTime(unixTime *C.struct_unix_time) time.Time {
	seconds := int64(unixTime.sec)
	nanos := int64(unixTime.nsec)
	return time.Unix(seconds, nanos)
}

// civisibility_initialize initializes the CI visibility integration.
//
//export civisibility_initialize
func civisibility_initialize(language *C.char, runtime_name *C.char, runtime_version *C.char, framework *C.char, framework_version *C.char, unix_start_time *C.struct_unix_time) {
	if runtime_name != nil {
		utils.AddCITags(constants.RuntimeName, C.GoString(runtime_name))
	}
	if runtime_version != nil {
		utils.AddCITags(constants.RuntimeVersion, C.GoString(runtime_version))
	}

	if language != nil {
		utils.AddCITags("language", C.GoString(language))
	} else {
		utils.AddCITags("language", "shared-lib")
	}

	civisibility.EnsureCiVisibilityInitialization()

	var sessionOptions []civisibility.TestSessionStartOption
	if framework != nil && framework_version != nil {
		sessionOptions = append(sessionOptions, civisibility.WithTestSessionFramework(C.GoString(framework), C.GoString(framework_version)))
	}
	if unix_start_time != nil {
		sessionOptions = append(sessionOptions, civisibility.WithTestSessionStartTime(getUnixTime(unix_start_time)))
	}

	session = civisibility.CreateTestSession(sessionOptions...)
}

// civisibility_session_set_string_tag sets a string tag on the session.
//
//export civisibility_session_set_string_tag
func civisibility_session_set_string_tag(key *C.char, value *C.char) C.uchar {
	if session != nil {
		session.SetTag(C.GoString(key), C.GoString(value))
		return 1
	}
	return 0
}

// civisibility_session_set_number_tag sets a number tag on the session.
//
//export civisibility_session_set_number_tag
func civisibility_session_set_number_tag(key *C.char, value C.double) C.uchar {
	if session != nil {
		session.SetTag(C.GoString(key), float64(value))
		return 1
	}
	return 0
}

// civisibility_session_set_error sets an error on the session.
//
//export civisibility_session_set_error
func civisibility_session_set_error(error_type *C.char, error_message *C.char, error_stacktrace *C.char) C.uchar {
	if session != nil {
		session.SetError(civisibility.WithErrorInfo(C.GoString(error_type), C.GoString(error_message), C.GoString(error_stacktrace)))
		return 1
	}
	return 0
}

// civisibility_shutdown shuts down the CI visibility integration.
//
//export civisibility_shutdown
func civisibility_shutdown(exit_code C.int, unix_finish_time *C.struct_unix_time) {
	if session != nil {
		if unix_finish_time != nil {
			session.Close(int(exit_code), civisibility.WithTestSessionFinishTime(getUnixTime(unix_finish_time)))
		} else {
			session.Close(int(exit_code))
		}
	}
	civisibility.ExitCiVisibility()
}

// ************************
// MODULES
// ************************

// civisibility_create_module creates a new module for the given name.
//
//export civisibility_create_module
func civisibility_create_module(name *C.char, framework *C.char, framework_version *C.char, unix_start_time *C.struct_unix_time) C.ulonglong {
	modulesMutex.Lock()
	defer modulesMutex.Unlock()
	var moduleOptions []civisibility.TestModuleStartOption
	if framework != nil && framework_version != nil {
		moduleOptions = append(moduleOptions, civisibility.WithTestModuleFramework(C.GoString(framework), C.GoString(framework_version)))
	}
	if unix_start_time != nil {
		moduleOptions = append(moduleOptions, civisibility.WithTestModuleStartTime(getUnixTime(unix_start_time)))
	}

	module := session.GetOrCreateModule(C.GoString(name), moduleOptions...)
	modules[module.ModuleID()] = module
	return C.ulonglong(module.ModuleID())
}

// civisibility_module_set_string_tag sets a string tag on the module.
//
//export civisibility_module_set_string_tag
func civisibility_module_set_string_tag(module_id C.ulonglong, key *C.char, value *C.char) C.uchar {
	modulesMutex.RLock()
	defer modulesMutex.RUnlock()
	if module, ok := modules[uint64(module_id)]; ok {
		module.SetTag(C.GoString(key), C.GoString(value))
		return 1
	}
	return 0
}

// civisibility_module_set_number_tag sets a number tag on the module.
//
//export civisibility_module_set_number_tag
func civisibility_module_set_number_tag(module_id C.ulonglong, key *C.char, value C.double) C.uchar {
	modulesMutex.RLock()
	defer modulesMutex.RUnlock()
	if module, ok := modules[uint64(module_id)]; ok {
		module.SetTag(C.GoString(key), float64(value))
		return 1
	}
	return 0
}

// civisibility_module_set_error sets an error on the module.
//
//export civisibility_module_set_error
func civisibility_module_set_error(module_id C.ulonglong, error_type *C.char, error_message *C.char, error_stacktrace *C.char) C.uchar {
	modulesMutex.RLock()
	defer modulesMutex.RUnlock()
	if module, ok := modules[uint64(module_id)]; ok {
		module.SetError(civisibility.WithErrorInfo(C.GoString(error_type), C.GoString(error_message), C.GoString(error_stacktrace)))
		return 1
	}
	return 0
}

// civisibility_close_module closes the module.
//
//export civisibility_close_module
func civisibility_close_module(module_id C.ulonglong, unix_finish_time *C.struct_unix_time) C.uchar {
	modulesMutex.Lock()
	defer modulesMutex.Unlock()
	moduleID := uint64(module_id)
	if module, ok := modules[moduleID]; ok {
		var moduleOptions []civisibility.TestModuleCloseOption
		if unix_finish_time != nil {
			moduleOptions = append(moduleOptions, civisibility.WithTestModuleFinishTime(getUnixTime(unix_finish_time)))
		}

		module.Close(moduleOptions...)
		delete(modules, moduleID)
		return 1
	}
	return 0
}

// ************************
// SUITES
// ************************

// civisibility_create_test_suite creates a new test suite for the given module.
//
//export civisibility_create_test_suite
func civisibility_create_test_suite(module_id C.ulonglong, name *C.char, unix_start_time *C.struct_unix_time) C.ulonglong {
	modulesMutex.RLock()
	defer modulesMutex.RUnlock()
	if module, ok := modules[uint64(module_id)]; ok {
		var suiteOptions []civisibility.TestSuiteStartOption
		if unix_start_time != nil {
			suiteOptions = append(suiteOptions, civisibility.WithTestSuiteStartTime(getUnixTime(unix_start_time)))
		}

		suitesMutex.Lock()
		defer suitesMutex.Unlock()
		testSuite := module.GetOrCreateSuite(C.GoString(name), suiteOptions...)
		suites[testSuite.SuiteID()] = testSuite
		return C.ulonglong(testSuite.SuiteID())
	}
	return 0
}

// civisibility_suite_set_string_tag sets a string tag on the suite.
//
//export civisibility_suite_set_string_tag
func civisibility_suite_set_string_tag(suite_id C.ulonglong, key *C.char, value *C.char) C.uchar {
	suitesMutex.RLock()
	defer suitesMutex.RUnlock()
	if suite, ok := suites[uint64(suite_id)]; ok {
		suite.SetTag(C.GoString(key), C.GoString(value))
		return 1
	}
	return 0
}

// civisibility_suite_set_number_tag sets a number tag on the suite.
//
//export civisibility_suite_set_number_tag
func civisibility_suite_set_number_tag(suite_id C.ulonglong, key *C.char, value C.double) C.uchar {
	suitesMutex.RLock()
	defer suitesMutex.RUnlock()
	if suite, ok := suites[uint64(suite_id)]; ok {
		suite.SetTag(C.GoString(key), float64(value))
		return 1
	}
	return 0
}

// civisibility_suite_set_error sets an error on the suite.
//
//export civisibility_suite_set_error
func civisibility_suite_set_error(suite_id C.ulonglong, error_type *C.char, error_message *C.char, error_stacktrace *C.char) C.uchar {
	suitesMutex.RLock()
	defer suitesMutex.RUnlock()
	if suite, ok := suites[uint64(suite_id)]; ok {
		suite.SetError(civisibility.WithErrorInfo(C.GoString(error_type), C.GoString(error_message), C.GoString(error_stacktrace)))
		return 1
	}
	return 0
}

// civisibility_close_test_suite closes the suite.
//
//export civisibility_close_test_suite
func civisibility_close_test_suite(suite_id C.ulonglong, unix_finish_time *C.struct_unix_time) C.uchar {
	suitesMutex.Lock()
	defer suitesMutex.Unlock()
	suiteID := uint64(suite_id)
	if suite, ok := suites[suiteID]; ok {
		var suiteOptions []civisibility.TestSuiteCloseOption
		if unix_finish_time != nil {
			suiteOptions = append(suiteOptions, civisibility.WithTestSuiteFinishTime(getUnixTime(unix_finish_time)))
		}

		suite.Close(suiteOptions...)
		delete(suites, suiteID)
		return 1
	}
	return 0
}

// ************************
// TESTS
// ************************

// civisibility_create_test creates a new test for the given suite.
//
//export civisibility_create_test
func civisibility_create_test(suite_id C.ulonglong, name *C.char, unix_start_time *C.struct_unix_time) C.ulonglong {
	suitesMutex.RLock()
	defer suitesMutex.RUnlock()
	if suite, ok := suites[uint64(suite_id)]; ok {
		var testOptions []civisibility.TestStartOption
		if unix_start_time != nil {
			testOptions = append(testOptions, civisibility.WithTestStartTime(getUnixTime(unix_start_time)))
		}

		testsMutex.Lock()
		defer testsMutex.Unlock()
		test := suite.CreateTest(C.GoString(name), testOptions...)
		tests[test.TestID()] = test
		return C.ulonglong(test.TestID())
	}
	return 0
}

// civisibility_test_set_string_tag sets a string tag on the test.
//
//export civisibility_test_set_string_tag
func civisibility_test_set_string_tag(test_id C.ulonglong, key *C.char, value *C.char) C.uchar {
	testsMutex.RLock()
	defer testsMutex.RUnlock()
	if test, ok := tests[uint64(test_id)]; ok {
		test.SetTag(C.GoString(key), C.GoString(value))
		return 1
	}
	return 0
}

// civisibility_test_set_number_tag sets a number tag on the test.
//
//export civisibility_test_set_number_tag
func civisibility_test_set_number_tag(test_id C.ulonglong, key *C.char, value C.double) C.uchar {
	testsMutex.RLock()
	defer testsMutex.RUnlock()
	if test, ok := tests[uint64(test_id)]; ok {
		test.SetTag(C.GoString(key), float64(value))
		return 1
	}
	return 0
}

// civisibility_test_set_error sets an error on the test.
//
//export civisibility_test_set_error
func civisibility_test_set_error(test_id C.ulonglong, error_type *C.char, error_message *C.char, error_stacktrace *C.char) C.uchar {
	testsMutex.RLock()
	defer testsMutex.RUnlock()
	if test, ok := tests[uint64(test_id)]; ok {
		test.SetError(civisibility.WithErrorInfo(C.GoString(error_type), C.GoString(error_message), C.GoString(error_stacktrace)))
		return 1
	}
	return 0
}

// civisibility_test_set_test_source sets the source file and line numbers for the test.
//
//export civisibility_test_set_test_source
func civisibility_test_set_test_source(test_id C.ulonglong, test_source_file *C.char, test_source_start_line *C.int, test_source_end_line *C.int) C.uchar {
	testsMutex.RLock()
	defer testsMutex.RUnlock()
	if test, ok := tests[uint64(test_id)]; ok {
		if test_source_file != nil {
			file := C.GoString(test_source_file)
			test.SetTag(constants.TestSourceFile, file)

			// get the codeowner of the function
			codeOwners := utils.GetCodeOwners()
			if codeOwners != nil {
				match, found := codeOwners.Match("/" + file)
				if found {
					test.SetTag(constants.TestCodeOwners, match.GetOwnersString())
				}
			}
		}
		if test_source_start_line != nil {
			test.SetTag(constants.TestSourceStartLine, int(*test_source_start_line))
		}
		if test_source_end_line != nil {
			test.SetTag(constants.TestSourceEndLine, int(*test_source_end_line))
		}
		return 1
	}
	return 0
}

// civisibility_close_test closes the test.
// status = 0: passed, 1: failed, 2: skipped
//
//export civisibility_close_test
func civisibility_close_test(test_id C.ulonglong, status C.uchar, skip_reason *C.char, unix_finish_time *C.struct_unix_time) C.uchar {
	testsMutex.Lock()
	defer testsMutex.Unlock()
	testID := uint64(test_id)
	if test, ok := tests[testID]; ok {
		var testOptions []civisibility.TestCloseOption
		if skip_reason != nil {
			testOptions = append(testOptions, civisibility.WithTestSkipReason(C.GoString(skip_reason)))
		}
		if unix_finish_time != nil {
			testOptions = append(testOptions, civisibility.WithTestFinishTime(getUnixTime(unix_finish_time)))
		}

		test.Close(civisibility.TestResultStatus(status), testOptions...)
		delete(tests, testID)
		return 1
	}
	return 0
}

// civisibility_get_settings gets the CI visibility settings.
//
//export civisibility_get_settings
func civisibility_get_settings() C.struct_settings_response {
	var cSettings C.struct_settings_response
	cSettings.code_coverage = 0
	cSettings.early_flake_detection.enabled = 0
	cSettings.early_flake_detection.slow_test_retries.ten_s = 0
	cSettings.early_flake_detection.slow_test_retries.thirty_s = 0
	cSettings.early_flake_detection.slow_test_retries.five_m = 0
	cSettings.early_flake_detection.slow_test_retries.five_s = 0
	cSettings.early_flake_detection.faulty_session_threshold = 0
	cSettings.flaky_test_retries_enabled = 0
	cSettings.itr_enabled = 0
	cSettings.require_git = 0
	cSettings.tests_skipping = 0

	settings := civisibility.GetSettings()
	if settings == nil {
		return cSettings
	}

	cSettings.code_coverage = convertToUChar(settings.CodeCoverage)
	cSettings.early_flake_detection.enabled = convertToUChar(settings.EarlyFlakeDetection.Enabled)
	cSettings.early_flake_detection.slow_test_retries.ten_s = C.int(settings.EarlyFlakeDetection.SlowTestRetries.TenS)
	cSettings.early_flake_detection.slow_test_retries.thirty_s = C.int(settings.EarlyFlakeDetection.SlowTestRetries.ThirtyS)
	cSettings.early_flake_detection.slow_test_retries.five_m = C.int(settings.EarlyFlakeDetection.SlowTestRetries.FiveM)
	cSettings.early_flake_detection.slow_test_retries.five_s = C.int(settings.EarlyFlakeDetection.SlowTestRetries.FiveS)
	cSettings.early_flake_detection.faulty_session_threshold = C.int(settings.EarlyFlakeDetection.FaultySessionThreshold)
	cSettings.flaky_test_retries_enabled = convertToUChar(settings.FlakyTestRetriesEnabled)
	cSettings.itr_enabled = convertToUChar(settings.ItrEnabled)
	cSettings.require_git = convertToUChar(settings.RequireGit)
	cSettings.tests_skipping = convertToUChar(settings.TestsSkipping)

	civisibility.GetEarlyFlakeDetectionSettings()
	return cSettings
}

// civisibility_get_flaky_test_retries_settings gets the flaky test retries settings.
//
//export civisibility_get_flaky_test_retries_settings
func civisibility_get_flaky_test_retries_settings() C.struct_flaky_test_retries_settings {
	var cSettings C.struct_flaky_test_retries_settings
	cSettings.retry_count = 0
	cSettings.total_retry_count = 0

	settings := civisibility.GetFlakyRetriesSettings()
	if settings == nil {
		return cSettings
	}

	cSettings.retry_count = C.int(settings.RetryCount)
	cSettings.total_retry_count = C.int(settings.TotalRetryCount)
	return cSettings
}

// civisibility_get_known_tests gets the known tests.
//
//export civisibility_get_known_tests
func civisibility_get_known_tests(length *C.int) *C.struct_known_test {
	var knownTests []C.struct_known_test
	for moduleName, module := range civisibility.GetEarlyFlakeDetectionSettings().Tests {
		for suiteName, suite := range module {
			for _, testName := range suite {
				knownTest := C.struct_known_test{
					module_name: C.CString(moduleName),
					suite_name:  C.CString(suiteName),
					test_name:   C.CString(testName),
				}
				knownTests = append(knownTests, knownTest)
			}
		}
	}

	*length = C.int(len(knownTests))
	fKnownTests := (unsafe.Pointer)(C.malloc(C.size_t(len(knownTests) * known_test_size)))

	for i, knownTest := range knownTests {
		c_known_test := unsafe.Add(fKnownTests, i*known_test_size)
		*(*C.struct_known_test)(c_known_test) = knownTest
	}
	return (*C.struct_known_test)(fKnownTests)
}

func convertToUChar(value bool) C.uchar {
	if value {
		return 1
	}
	return 0
}

func main() {}
