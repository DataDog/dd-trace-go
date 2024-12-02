// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build civisibility
// +build civisibility

// go build -tags civisibility -buildmode=c-shared -ldflags "-s -w" -o libcivisibility.dylib civisibility_exports.go

package main

import "C"
import (
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	civisibility "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils"
)

var session civisibility.TestSession

var modulesMutex sync.RWMutex
var modules = make(map[uint64]civisibility.TestModule)

var suitesMutex sync.RWMutex
var suites = make(map[uint64]civisibility.TestSuite)

var testsMutex sync.RWMutex
var tests = make(map[uint64]civisibility.Test)

// civisibility_initialize initializes the CI visibility integration.
//
//export civisibility_initialize
func civisibility_initialize(runtime_name *C.char, runtime_version *C.char, framework *C.char, framework_version *C.char, unix_start_time *C.longlong) {
	if runtime_name != nil {
		utils.AddCITags(constants.RuntimeName, C.GoString(runtime_name))
	}
	if runtime_version != nil {
		utils.AddCITags(constants.RuntimeVersion, C.GoString(runtime_version))
	}

	utils.AddCITags("language", "shared-lib")
	civisibility.EnsureCiVisibilityInitialization()

	var sessionOptions []civisibility.TestSessionStartOption
	if framework != nil && framework_version != nil {
		sessionOptions = append(sessionOptions, civisibility.WithTestSessionFramework(C.GoString(framework), C.GoString(framework_version)))
	}
	if unix_start_time != nil {
		sessionOptions = append(sessionOptions, civisibility.WithTestSessionStartTime(time.Unix(int64(*unix_start_time), 0)))
	}

	session = civisibility.CreateTestSession(sessionOptions...)
}

// civisibility_shutdown shuts down the CI visibility integration.
//
//export civisibility_shutdown
func civisibility_shutdown(exit_code C.int, unix_finish_time *C.longlong) {
	if session != nil {
		if unix_finish_time != nil {
			session.Close(int(exit_code), civisibility.WithTestSessionFinishTime(time.Unix(int64(*unix_finish_time), 0)))
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
func civisibility_create_module(name *C.char, framework *C.char, framework_version *C.char, unix_start_time *C.longlong) C.ulonglong {
	modulesMutex.Lock()
	defer modulesMutex.Unlock()
	var moduleOptions []civisibility.TestModuleStartOption
	if framework != nil && framework_version != nil {
		moduleOptions = append(moduleOptions, civisibility.WithTestModuleFramework(C.GoString(framework), C.GoString(framework_version)))
	}
	if unix_start_time != nil {
		moduleOptions = append(moduleOptions, civisibility.WithTestModuleStartTime(time.Unix(int64(*unix_start_time), 0)))
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
func civisibility_close_module(module_id C.ulonglong, unix_finish_time *C.longlong) C.uchar {
	modulesMutex.Lock()
	defer modulesMutex.Unlock()
	moduleID := uint64(module_id)
	if module, ok := modules[moduleID]; ok {
		var moduleOptions []civisibility.TestModuleCloseOption
		if unix_finish_time != nil {
			moduleOptions = append(moduleOptions, civisibility.WithTestModuleFinishTime(time.Unix(int64(*unix_finish_time), 0)))
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
func civisibility_create_test_suite(module_id C.ulonglong, name *C.char, unix_start_time *C.longlong) C.ulonglong {
	modulesMutex.RLock()
	defer modulesMutex.RUnlock()
	if module, ok := modules[uint64(module_id)]; ok {
		var suiteOptions []civisibility.TestSuiteStartOption
		if unix_start_time != nil {
			suiteOptions = append(suiteOptions, civisibility.WithTestSuiteStartTime(time.Unix(int64(*unix_start_time), 0)))
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
func civisibility_close_test_suite(suite_id C.ulonglong, unix_finish_time *C.longlong) C.uchar {
	suitesMutex.Lock()
	defer suitesMutex.Unlock()
	suiteID := uint64(suite_id)
	if suite, ok := suites[suiteID]; ok {
		var suiteOptions []civisibility.TestSuiteCloseOption
		if unix_finish_time != nil {
			suiteOptions = append(suiteOptions, civisibility.WithTestSuiteFinishTime(time.Unix(int64(*unix_finish_time), 0)))
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
func civisibility_create_test(suite_id C.ulonglong, name *C.char, unix_start_time *C.longlong) C.ulonglong {
	suitesMutex.RLock()
	defer suitesMutex.RUnlock()
	if suite, ok := suites[uint64(suite_id)]; ok {
		var testOptions []civisibility.TestStartOption
		if unix_start_time != nil {
			testOptions = append(testOptions, civisibility.WithTestStartTime(time.Unix(int64(*unix_start_time), 0)))
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
func civisibility_close_test(test_id C.ulonglong, status C.uchar, unix_finish_time *C.longlong) C.uchar {
	testsMutex.Lock()
	defer testsMutex.Unlock()
	testID := uint64(test_id)
	if test, ok := tests[testID]; ok {
		var testOptions []civisibility.TestCloseOption
		if unix_finish_time != nil {
			testOptions = append(testOptions, civisibility.WithTestFinishTime(time.Unix(int64(*unix_finish_time), 0)))
		}

		test.Close(civisibility.TestResultStatus(status), testOptions...)
		delete(tests, testID)
		return 1
	}
	return 0
}

func main() {}
