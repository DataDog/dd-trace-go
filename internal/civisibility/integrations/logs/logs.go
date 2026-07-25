// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

import (
	"os"
	"strconv"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/hostname"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

var (
	// logsMu protects process-wide log configuration and writer lifecycle state.
	logsMu locking.Mutex

	// logsInitMu serializes first-use configuration resolution without holding
	// logsMu across provider work.
	logsInitMu locking.Mutex

	// logsWriterInstance is the singleton instance of logsWriter.
	logsWriterInstance *logsWriter

	// servName is the name of the service for which logs are being written.
	servName string

	// host is the hostname of the machine where the logs are being written.
	host string

	// enabled indicates whether the logs writer is enabled.
	enabled *bool

	enabledReporter func()
	enabledReported bool

	prepareLogsEnabledConfig = internalconfig.PrepareCIVisibilityLogsEnabled
	reportLogsConfigEvents   = internalconfig.ReportCIVisibilityConfigEvents
	prepareLogsWriterFunc    = prepareLogsWriter
)

func IsEnabled() bool {
	value, report := PrepareEnabled()
	report()
	return value
}

// PrepareEnabled resolves and publishes log enablement, returning an
// idempotent reporter that never runs while an initialization lock is held.
func PrepareEnabled() (bool, func()) {
	logsMu.Lock()
	if enabled != nil {
		value := *enabled
		logsMu.Unlock()
		return value, reportEnabledConfig
	}
	logsMu.Unlock()

	logsInitMu.Lock()
	logsMu.Lock()
	if enabled != nil {
		value := *enabled
		logsMu.Unlock()
		logsInitMu.Unlock()
		return value, reportEnabledConfig
	}
	logsMu.Unlock()

	value, events := prepareLogsEnabledConfig()
	logsMu.Lock()
	enabled = &value
	enabledReporter = func() { reportLogsConfigEvents(events) }
	logsMu.Unlock()
	logsInitMu.Unlock()
	return value, reportEnabledConfig
}

func reportEnabledConfig() {
	logsMu.Lock()
	shouldReport := !enabledReported
	if shouldReport {
		enabledReported = true
	}
	report := enabledReporter
	logsMu.Unlock()
	if shouldReport && report != nil {
		report()
	}
}

// Initialize initializes the logs writer for CI visibility.
func Initialize(serviceName string) {
	report := PrepareInitialize(serviceName)
	report()
}

// PrepareInitialize publishes the logs writer before returning its deferred
// configuration reporter.
func PrepareInitialize(serviceName string) func() {
	isEnabled, report := PrepareEnabled()
	if !isEnabled {
		return report
	}
	logsMu.Lock()
	if logsWriterInstance != nil {
		logsMu.Unlock()
		return report
	}
	resolvedHost := hostname.Get()
	if resolvedHost == "" {
		resolvedHost, _ = os.Hostname()
	}
	writer, reportWriter := prepareLogsWriterFunc()

	servName = serviceName
	host = resolvedHost
	logsWriterInstance = writer
	logsMu.Unlock()
	return func() {
		report()
		reportWriter()
	}
}

// Stop stops the logs writer and cleans up resources.
func Stop() {
	if !IsEnabled() {
		return
	}
	logsMu.Lock()
	if logsWriterInstance == nil {
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
	if !IsEnabled() {
		return
	}
	logsMu.Lock()
	if logsWriterInstance == nil {
		logsMu.Unlock()
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
	logsMu.Unlock()
}
