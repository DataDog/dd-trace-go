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
typedef unsigned long long SessionID;
typedef unsigned long long ModuleID;
typedef unsigned long long SuiteID;
typedef unsigned long long TestID;

typedef struct {
    char* key;
    char* value;
} KeyValuePair;
const int KeyValuePair_Size = sizeof(KeyValuePair);

typedef struct {
    KeyValuePair* data;
    unsigned long long len;
} KeyValueArray;
const int KeyValueArray_Size = sizeof(KeyValueArray);

typedef struct {
    char* language;
    char* runtime_name;
    char* runtime_version;
    char* working_directory;
    KeyValueArray* environment_variables;
	// Unused fields
	void* unused01;
	void* unused02;
	void* unused03;
	void* unused04;
	void* unused05;
} InitSettings;
const int InitSettings_Size = sizeof(InitSettings);

typedef struct {
    unsigned long long sec;
    unsigned long long nsec;
} UnixTime;
const int UnixTime_Size = sizeof(UnixTime);

typedef struct {
	int ten_s;
	int thirty_s;
	int five_m;
	int five_s;
} SettingsEarlyFlakeDetectionSlowRetries;
const int SettingsEarlyFlakeDetectionSlowRetries_Size = sizeof(SettingsEarlyFlakeDetectionSlowRetries);

typedef struct {
	Bool enabled;
	SettingsEarlyFlakeDetectionSlowRetries slow_test_retries;
	int faulty_session_threshold;
} SettingsEarlyFlakeDetection;
const int SettingsEarlyFlakeDetection_Size = sizeof(SettingsEarlyFlakeDetection);

typedef struct {
	Bool code_coverage;
	SettingsEarlyFlakeDetection early_flake_detection;
	Bool flaky_test_retries_enabled;
	Bool itr_enabled;
	Bool require_git;
	Bool tests_skipping;
} SettingsResponse;
const int SettingsResponse_Size = sizeof(SettingsResponse);

typedef struct {
	int retry_count;
	int total_retry_count;
} FlakyTestRetriesSettings;
const int FlakyTestRetriesSettings_Size = sizeof(FlakyTestRetriesSettings);

typedef struct {
	char* module_name;
	char* suite_name;
	char* test_name;
} KnownTest;
const int KnownTest_Size = sizeof(KnownTest);

typedef struct {
	char* suite_name;
	char* test_name;
	char* parameters;
	char* custom_configurations_json;
} SkippableTest;
const int SkippableTest_Size = sizeof(SkippableTest);

typedef struct {
	char* filename;
	char* bitmap;
} TestCoverageFile;
const int TestCoverageFile_Size = sizeof(TestCoverageFile);

typedef struct {
	SuiteID test_suite_id;
	TestID span_id;
	TestCoverageFile* files;
	unsigned long long files_len;
} TestCoverage;
const int TestCoverage_Size = sizeof(TestCoverage);
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
func getUnixTime(unixTime *C.UnixTime) time.Time {
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
// Initialization
// *******************************************************************************************************************

var (
	hasInitialized atomic.Bool // indicate if the library has been initialized
	canShutdown    atomic.Bool // indicate if the library can be shut down
)

// test_optimization_initialize initializes the library with the given settings.
//
//export test_optimization_initialize
func test_optimization_initialize(settings *C.InitSettings) C.Bool {
	if hasInitialized.Swap(true) {
		return toBool(false)
	}

	canShutdown.Store(true)
	if settings != nil {
		if settings.environment_variables != nil {
			sLen := int(settings.environment_variables.len)
			kvSize := int(C.KeyValuePair_Size)
			for i := 0; i < sLen; i++ {
				keyValue := (*C.KeyValuePair)(unsafe.Add(unsafe.Pointer(settings.environment_variables.data), i*kvSize))
				os.Setenv(C.GoString(keyValue.key), C.GoString(keyValue.value))
			}
		}
		if settings.working_directory != nil {
			wd := C.GoString(settings.working_directory)
			if wd != "" {
				if currentDir, err := os.Getwd(); err == nil {
					defer func() {
						os.Chdir(currentDir)
					}()
				}
				os.Chdir(wd)
			}
		}
		if settings.runtime_name != nil {
			utils.AddCITags(constants.RuntimeName, C.GoString(settings.runtime_name))
		}
		if settings.runtime_version != nil {
			utils.AddCITags(constants.RuntimeVersion, C.GoString(settings.runtime_version))
		}
		if settings.language != nil {
			utils.AddCITags("language", C.GoString(settings.language))
		} else {
			utils.AddCITags("language", "native")
		}
	} else {
		utils.AddCITags("language", "native")
	}

	civisibility.EnsureCiVisibilityInitialization()
	return toBool(true)
}

// test_optimization_shutdown shuts down the library.
//
//export test_optimization_shutdown
func test_optimization_shutdown() C.Bool {
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

// test_optimization_session_create creates a new test session.
//
//export test_optimization_session_create
func test_optimization_session_create(framework *C.char, framework_version *C.char, unix_start_time *C.UnixTime) C.SessionID {
	var sessionOptions []civisibility.TestSessionStartOption
	if framework != nil {
		goFramework := C.GoString(framework)
		goFrameworkVersion := ""
		if framework_version != nil {
			goFrameworkVersion = C.GoString(framework_version)
		}

		sessionOptions = append(sessionOptions, civisibility.WithTestSessionFramework(goFramework, goFrameworkVersion))
	}
	if unix_start_time != nil {
		sessionOptions = append(sessionOptions, civisibility.WithTestSessionStartTime(getUnixTime(unix_start_time)))
	}

	session := civisibility.CreateTestSession(sessionOptions...)
	id := session.SessionID()

	sessionMutex.Lock()
	defer sessionMutex.Unlock()
	sessions[id] = session
	return C.SessionID(id)
}

// test_optimization_session_close closes the test session with the given ID.
//
//export test_optimization_session_close
func test_optimization_session_close(session_id C.SessionID, exit_code C.int, unix_finish_time *C.UnixTime) C.Bool {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()
	if session, ok := sessions[uint64(session_id)]; ok {
		if unix_finish_time != nil {
			session.Close(int(exit_code), civisibility.WithTestSessionFinishTime(getUnixTime(unix_finish_time)))
		} else {
			session.Close(int(exit_code))
		}
		return toBool(true)
	}
	return toBool(false)
}
