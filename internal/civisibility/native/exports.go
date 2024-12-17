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

typedef struct {
    char* key;
    char* value;
} topt_KeyValuePair;
const int topt_KeyValuePair_Size = sizeof(topt_KeyValuePair_Size);

typedef struct {
    topt_KeyValuePair* data;
    Uint64 len;
} topt_KeyValueArray;
const int topt_KeyValueArray_Size = sizeof(topt_KeyValueArray);

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

typedef struct {
    Uint64 sec;
    Uint64 nsec;
} topt_UnixTime;
const int topt_UnixTime_Size = sizeof(topt_UnixTime);

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
// Initialization
// *******************************************************************************************************************

var (
	hasInitialized atomic.Bool // indicate if the library has been initialized
	canShutdown    atomic.Bool // indicate if the library can be shut down
)

// topt_initialize initializes the library with the given options.
//
//export topt_initialize
func topt_initialize(options *C.topt_InitOptions) C.Bool {
	if hasInitialized.Swap(true) {
		return toBool(false)
	}

	canShutdown.Store(true)
	var tags map[string]string
	if options != nil {
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
	} else {
		tags["language"] = "native"
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

// topt_session_create creates a new test session.
//
//export topt_session_create
func topt_session_create(framework *C.char, framework_version *C.char, unix_start_time *C.topt_UnixTime) C.topt_SessionId {
	var sessionOptions []civisibility.TestSessionStartOption
	if framework != nil {
		sessionOptions = append(sessionOptions, civisibility.WithTestSessionFramework(C.GoString(framework), C.GoString(framework_version)))
	}
	if unix_start_time != nil {
		sessionOptions = append(sessionOptions, civisibility.WithTestSessionStartTime(getUnixTime(unix_start_time)))
	}

	session := civisibility.CreateTestSession(sessionOptions...)
	id := session.SessionID()

	sessionMutex.Lock()
	defer sessionMutex.Unlock()
	sessions[id] = session
	return C.topt_SessionId(id)
}

// topt_session_close closes the test session with the given ID.
//
//export topt_session_close
func topt_session_close(session_id C.topt_SessionId, exit_code C.int, unix_finish_time *C.topt_UnixTime) C.Bool {
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
