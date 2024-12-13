// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

//go:build civisibility_android
// +build civisibility_android

// gomobile bind -work  -tags civisibility_android -v -o civisibility.aar -target=android -androidapi 21 ./

package civisibility

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
struct skippable_test {
	char* suite_name;
	char* test_name;
	char* parameters;
	char* custom_configurations_json;
};
struct test_coverage_file {
	char* filename;
};
struct test_coverage {
	unsigned long long test_suite_id;
	unsigned long long span_id;
	struct test_coverage_file* files;
	unsigned long long files_len;
};
*/
import "C"
import (
	"bytes"
	"encoding/json"
	"os"
	"sync"
	"time"
	"unsafe"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	civisibility "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/net"
)

const (
	known_test_size         = 3 * int(unsafe.Sizeof(uintptr(0)))
	skippable_test_size     = 4 * int(unsafe.Sizeof(uintptr(0)))
	test_coverage_size      = (4 * 8) + (1 * int(unsafe.Sizeof(uintptr(0))))
	test_coverage_file_size = int(unsafe.Sizeof(uintptr(0)))
)

var (
	session civisibility.TestSession

	modulesMutex sync.RWMutex
	modules      = make(map[uint64]civisibility.TestModule)

	suitesMutex sync.RWMutex
	suites      = make(map[uint64]civisibility.TestSuite)

	testsMutex sync.RWMutex
	tests      = make(map[uint64]civisibility.Test)

	client = net.NewClientForCodeCoverage()
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
	}
)

func getUnixTime(unixTime *C.struct_unix_time) time.Time {
	seconds := int64(unixTime.sec)
	nanos := int64(unixTime.nsec)
	return time.Unix(seconds, nanos)
}

// Initialize initializes the CI visibility integration.
//
//export Initialize
func Initialize(language, runtime_name, runtime_version, framework, framework_version string, unix_start_time_sec, unix_start_time_nsec int64) {
	os.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "1")
	os.Setenv("DD_TRACE_DEBUG", "1")

	if runtime_name != "" {
		utils.AddCITags(constants.RuntimeName, runtime_name)
	}
	if runtime_version != "" {
		utils.AddCITags(constants.RuntimeVersion, runtime_version)
	}

	if language != "" {
		utils.AddCITags("language", language)
	} else {
		utils.AddCITags("language", "shared-lib")
	}

	civisibility.EnsureCiVisibilityInitialization()

	var sessionOptions []civisibility.TestSessionStartOption
	if framework != "" && framework_version != "" {
		sessionOptions = append(sessionOptions, civisibility.WithTestSessionFramework(framework, framework_version))
	}
	if unix_start_time_sec != 0 {
		sessionOptions = append(sessionOptions, civisibility.WithTestSessionStartTime(time.Unix(unix_start_time_sec, unix_start_time_nsec)))
	}

	session = civisibility.CreateTestSession(sessionOptions...)
}

// Session_set_string_tag sets a string tag on the session.
//
//export Session_set_string_tag
func Session_set_string_tag(key, value string) bool {
	if session != nil {
		session.SetTag(key, value)
		return true
	}
	return false
}

// Session_set_number_tag sets a number tag on the session.
//
//export Session_set_number_tag
func Session_set_number_tag(key string, value float64) bool {
	if session != nil {
		session.SetTag(key, value)
		return true
	}
	return false
}

// Session_set_error sets an error on the session.
//
//export Session_set_error
func Session_set_error(error_type, error_message, error_stacktrace string) bool {
	if session != nil {
		session.SetError(civisibility.WithErrorInfo(error_type, error_message, error_stacktrace))
		return true
	}
	return false
}

// Shutdown shuts down the CI visibility integration.
//
//export Shutdown
func Shutdown(exit_code int, unix_start_time_sec, unix_start_time_nsec int64) {
	if session != nil {
		if unix_start_time_sec != 0 {
			session.Close(exit_code, civisibility.WithTestSessionFinishTime(time.Unix(unix_start_time_sec, unix_start_time_nsec)))
		} else {
			session.Close(exit_code)
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
	var moduleOptions []civisibility.TestModuleStartOption
	if framework != nil && framework_version != nil {
		moduleOptions = append(moduleOptions, civisibility.WithTestModuleFramework(C.GoString(framework), C.GoString(framework_version)))
	}
	if unix_start_time != nil {
		moduleOptions = append(moduleOptions, civisibility.WithTestModuleStartTime(getUnixTime(unix_start_time)))
	}

	module := session.GetOrCreateModule(C.GoString(name), moduleOptions...)

	modulesMutex.Lock()
	defer modulesMutex.Unlock()
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
	var moduleOptions []civisibility.TestModuleCloseOption
	if unix_finish_time != nil {
		moduleOptions = append(moduleOptions, civisibility.WithTestModuleFinishTime(getUnixTime(unix_finish_time)))
	}
	moduleID := uint64(module_id)

	modulesMutex.Lock()
	defer modulesMutex.Unlock()
	if module, ok := modules[moduleID]; ok {
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
	var suiteOptions []civisibility.TestSuiteStartOption
	if unix_start_time != nil {
		suiteOptions = append(suiteOptions, civisibility.WithTestSuiteStartTime(getUnixTime(unix_start_time)))
	}

	modulesMutex.RLock()
	defer modulesMutex.RUnlock()
	if module, ok := modules[uint64(module_id)]; ok {
		testSuite := module.GetOrCreateSuite(C.GoString(name), suiteOptions...)

		suitesMutex.Lock()
		defer suitesMutex.Unlock()
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
	var suiteOptions []civisibility.TestSuiteCloseOption
	if unix_finish_time != nil {
		suiteOptions = append(suiteOptions, civisibility.WithTestSuiteFinishTime(getUnixTime(unix_finish_time)))
	}
	suiteID := uint64(suite_id)

	suitesMutex.Lock()
	defer suitesMutex.Unlock()
	if suite, ok := suites[suiteID]; ok {
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
	var testOptions []civisibility.TestStartOption
	if unix_start_time != nil {
		testOptions = append(testOptions, civisibility.WithTestStartTime(getUnixTime(unix_start_time)))
	}

	suitesMutex.RLock()
	defer suitesMutex.RUnlock()
	if suite, ok := suites[uint64(suite_id)]; ok {
		test := suite.CreateTest(C.GoString(name), testOptions...)

		testsMutex.Lock()
		defer testsMutex.Unlock()
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
			file = utils.GetRelativePathFromCITagsSourceRoot(file)
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
	var testOptions []civisibility.TestCloseOption
	if skip_reason != nil {
		testOptions = append(testOptions, civisibility.WithTestSkipReason(C.GoString(skip_reason)))
	}
	if unix_finish_time != nil {
		testOptions = append(testOptions, civisibility.WithTestFinishTime(getUnixTime(unix_finish_time)))
	}
	testID := uint64(test_id)

	testsMutex.Lock()
	defer testsMutex.Unlock()
	if test, ok := tests[testID]; ok {
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
func civisibility_get_known_tests(known_tests **C.struct_known_test) C.int {
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

	fKnownTests := (unsafe.Pointer)(C.malloc(C.size_t(len(knownTests) * known_test_size)))
	for i, knownTest := range knownTests {
		c_known_test := unsafe.Add(fKnownTests, i*known_test_size)
		*(*C.struct_known_test)(c_known_test) = knownTest
	}

	*known_tests = (*C.struct_known_test)(fKnownTests)
	return C.int(len(knownTests))
}

// civisibility_get_skippable_tests gets the skippable tests.
//
//export civisibility_get_skippable_tests
func civisibility_get_skippable_tests(skippable_tests **C.struct_skippable_test) C.int {
	var skippableTests []C.struct_skippable_test
	for kSuite, suites := range civisibility.GetSkippableTests() {
		for kTest, test := range suites {
			for _, skippableTest := range test {
				var custom_config string
				if skippableTest.Configurations.Custom != nil {
					jsonBytes, _ := json.Marshal(skippableTest.Configurations.Custom)
					custom_config = string(jsonBytes)
				}
				skippableTest := C.struct_skippable_test{
					suite_name:                 C.CString(kSuite),
					test_name:                  C.CString(kTest),
					parameters:                 C.CString(skippableTest.Parameters),
					custom_configurations_json: C.CString(custom_config),
				}
				skippableTests = append(skippableTests, skippableTest)
			}
		}
	}

	fSkippableTests := (unsafe.Pointer)(C.malloc(C.size_t(len(skippableTests) * skippable_test_size)))

	for i, skippableTest := range skippableTests {
		c_skippable_test := unsafe.Add(fSkippableTests, i*skippable_test_size)
		*(*C.struct_skippable_test)(c_skippable_test) = skippableTest
	}

	*skippable_tests = (*C.struct_skippable_test)(fSkippableTests)
	return C.int(len(skippableTests))
}

// civisibility_send_code_coverage_payload sends the code coverage payload.
//
//export civisibility_send_code_coverage_payload
func civisibility_send_code_coverage_payload(coverages *C.struct_test_coverage, coverages_length C.int) {
	covLength := int(coverages_length)
	coveragePayload := ciTestCovPayload{
		Version: 2,
	}
	for i := 0; i < covLength; i++ {
		coverage := *(*C.struct_test_coverage)(unsafe.Add(unsafe.Pointer(coverages), i*test_coverage_size))
		coverageFilesLen := int(coverage.files_len)
		coverageData := ciTestCoverageData{
			SessionID: session.SessionID(),
			SuiteID:   uint64(coverage.test_suite_id),
			SpanID:    uint64(coverage.span_id),
		}
		for j := 0; j < coverageFilesLen; j++ {
			file := *(*C.struct_test_coverage_file)(unsafe.Add(unsafe.Pointer(coverage.files), j*test_coverage_file_size))
			coverageFile := ciTestCoverageFile{FileName: C.GoString(file.filename)}
			coverageFile.FileName = utils.GetRelativePathFromCITagsSourceRoot(coverageFile.FileName)
			coverageData.Files = append(coverageData.Files, coverageFile)
		}
		coveragePayload.Coverages = append(coveragePayload.Coverages, coverageData)
	}

	if covLength > 0 {
		// Create a new buffer to encode the coverage payload in MessagePack format
		encodedBuf := new(bytes.Buffer)
		jsonbytes, err := json.Marshal(&coveragePayload)
		if err == nil {
			encodedBuf.Write(jsonbytes)
			client.SendCoveragePayloadWithFormat(encodedBuf, net.FormatJSON)
		}
	}
}

func convertToUChar(value bool) C.uchar {
	if value {
		return 1
	}
	return 0
}
