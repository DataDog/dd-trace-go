// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

import (
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/stableconfig"

	"github.com/DataDog/dd-trace-go/v2/internal/hostname"
)

const (
	logsEnabledUnknown uint32 = iota
	logsEnabledFalse
	logsEnabledTrue
)

var (
	// logsMu protects process-wide log configuration and writer lifecycle state.
	logsMu sync.Mutex

	// logsWriterInstance is the singleton instance of logsWriter.
	logsWriterInstance *logsWriter

	// servName is the name of the service for which logs are being written.
	servName string

	// host is the hostname of the machine where the logs are being written.
	host string

	// enabledState caches immutable log enablement without taking the writer lifecycle lock.
	enabledState atomic.Uint32
)

func IsEnabled() bool {
	if enabled, ok := cachedEnabled(); ok {
		return enabled
	}
	logsMu.Lock()
	defer logsMu.Unlock()
	return isEnabledLocked()
}

func cachedEnabled() (bool, bool) {
	switch enabledState.Load() {
	case logsEnabledFalse:
		return false, true
	case logsEnabledTrue:
		return true, true
	default:
		return false, false
	}
}

// isEnabledLocked reports whether CI Visibility logs are enabled.
// logsMu must be held by the caller.
func isEnabledLocked() bool {
	if enabled, ok := cachedEnabled(); ok {
		return enabled
	}
	v, _, _ := stableconfig.Bool("DD_CIVISIBILITY_LOGS_ENABLED", false)
	if v {
		enabledState.Store(logsEnabledTrue)
	} else {
		enabledState.Store(logsEnabledFalse)
	}
	return v
}

// Initialize initializes the logs writer for CI visibility.
func Initialize(serviceName string) {
	logsMu.Lock()
	defer logsMu.Unlock()

	if !isEnabledLocked() || logsWriterInstance != nil {
		return
	}

	servName = serviceName
	host = hostname.Get()
	if host == "" {
		host, _ = os.Hostname()
	}
	logsWriterInstance = newLogsWriter()
}

// Stop stops the logs writer and cleans up resources.
func Stop() {
	logsMu.Lock()
	if !isEnabledLocked() || logsWriterInstance == nil {
		logsMu.Unlock()
		return
	}

	writer := logsWriterInstance
	logsWriterInstance = nil
	logsMu.Unlock()

	writer.stop()
}

// WriteLog writes a log entry with the given message and tags.
func WriteLog(testID uint64, moduleName string, suiteName string, testName string, message string, tags string) {
	logsMu.Lock()
	defer logsMu.Unlock()

	if !isEnabledLocked() || logsWriterInstance == nil {
		return
	}

	testIDStr := strconv.FormatUint(testID, 10)
	logsWriterInstance.add(&logEntry{
		DdSource:   "testoptimization",
		Hostname:   host,
		Timestamp:  time.Now().UnixMilli(),
		Message:    message,
		DdTraceID:  testIDStr,
		DdSpanID:   testIDStr,
		TestModule: moduleName,
		TestSuite:  suiteName,
		TestName:   testName,
		Service:    servName,
		DdTags:     tags,
	})
}
