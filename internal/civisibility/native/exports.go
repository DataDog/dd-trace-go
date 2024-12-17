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
struct key_value_array {
    struct key_value_pair* data;
    unsigned long long len;
};
extern const int key_value_array_size;
const int key_value_array_size = sizeof(struct key_value_array);

struct key_value_pair {
    char* key;
    char* value;
};
extern const int key_value_pair_size;
const int key_value_pair_size = sizeof(struct key_value_pair);

struct init_settings {
    char* language;
    char* runtime_name;
    char* runtime_version;
    char* working_directory;
    struct key_value_array* environment_variables;
	// Unused fields
	void* unused01;
	void* unused02;
	void* unused03;
	void* unused04;
	void* unused05;
};
extern const int init_settings_size;
const int init_settings_size = sizeof(struct init_settings);

struct unix_time {
    unsigned long long sec;
    unsigned long long nsec;
};
extern const int unix_time_size;
const int unix_time_size = sizeof(struct unix_time);

struct setting_early_flake_detection_slow_test_retries {
	int ten_s;
	int thirty_s;
	int five_m;
	int five_s;
};
extern const int setting_early_flake_detection_slow_test_retries_size;
const int setting_early_flake_detection_slow_test_retries_size = sizeof(struct setting_early_flake_detection_slow_test_retries);

struct setting_early_flake_detection {
	unsigned char enabled;
	struct setting_early_flake_detection_slow_test_retries slow_test_retries;
	int faulty_session_threshold;
};
extern const int setting_early_flake_detection_size;
const int setting_early_flake_detection_size = sizeof(struct setting_early_flake_detection);

struct settings_response {
	unsigned char code_coverage;
	struct setting_early_flake_detection early_flake_detection;
	unsigned char flaky_test_retries_enabled;
	unsigned char itr_enabled;
	unsigned char require_git;
	unsigned char tests_skipping;
};
extern const int settings_response_size;
const int settings_response_size = sizeof(struct settings_response);

struct flaky_test_retries_settings {
	int retry_count;
	int total_retry_count;
};
extern const int flaky_test_retries_settings_size;
const int flaky_test_retries_settings_size = sizeof(struct flaky_test_retries_settings);

struct known_test {
	char* module_name;
	char* suite_name;
	char* test_name;
};
extern const int known_test_size;
const int known_test_size = sizeof(struct known_test);

struct skippable_test {
	char* suite_name;
	char* test_name;
	char* parameters;
	char* custom_configurations_json;
};
extern const int skippable_test_size;
const int skippable_test_size = sizeof(struct skippable_test);

struct test_coverage_file {
	char* filename;
	char* bitmap;
};
extern const int test_coverage_file_size;
const int test_coverage_file_size = sizeof(struct test_coverage_file);

struct test_coverage {
	unsigned long long test_suite_id;
	unsigned long long span_id;
	struct test_coverage_file* files;
	unsigned long long files_len;
};
extern const int test_coverage_size;
const int test_coverage_size = sizeof(struct test_coverage);

typedef unsigned char bool;
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

// getUnixTime converts a C.struct_unix_time to Go's time.Time.
// If unixTime is nil, returns time.Now() as a fallback.
func getUnixTime(unixTime *C.struct_unix_time) time.Time {
	// If pointer is nil, provide a fallback time.
	if unixTime == nil {
		return time.Now()
	}
	return time.Unix(int64(unixTime.sec), int64(unixTime.nsec))
}

// toBool converts a Go bool to a C.bool (0 or 1).
func toBool(value bool) C.bool {
	if value {
		return C.bool(1)
	}
	return C.bool(0)
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
func test_optimization_initialize(settings *C.struct_init_settings) C.bool {
	if hasInitialized.Swap(true) {
		return toBool(false)
	}

	canShutdown.Store(true)
	if settings != nil {
		if settings.environment_variables != nil {
			sLen := int(settings.environment_variables.len)
			kvSize := int(C.key_value_pair_size)
			for i := 0; i < sLen; i++ {
				keyValue := (*C.struct_key_value_pair)(unsafe.Add(unsafe.Pointer(settings.environment_variables.data), i*kvSize))
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
func test_optimization_shutdown() C.bool {
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
func test_optimization_session_create(framework *C.char, framework_version *C.char, unix_start_time *C.struct_unix_time) C.ulonglong {
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
	return C.ulonglong(id)
}

// test_optimization_session_close closes the test session with the given ID.
//
//export test_optimization_session_close
func test_optimization_session_close(session_id C.ulonglong, exit_code C.int, unix_finish_time *C.struct_unix_time) C.bool {
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
