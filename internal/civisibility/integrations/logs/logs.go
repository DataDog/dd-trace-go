// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

import (
	"github.com/DataDog/dd-trace-go/v2/internal/stableconfig"
	"os"
	"strconv"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/hostname"
)

var (
	// logsWriterInstance is the singleton instance of logsWriter.
	logsWriterInstance *logsWriter

	// servName is the name of the service for which logs are being written.
	servName string

	// host is the hostname of the machine where the logs are being written.
	host string

	// enabled indicates whether the logs writer is enabled.
	enabled *bool
)

func IsEnabled() bool {
	if enabled == nil {
		v, _, _ := stableconfig.Bool("DD_CIVISIBILITY_LOGS_ENABLED", false)
		enabled = &v
	}

	return *enabled
}

// Initialize initializes the logs writer for CI visibility.
func Initialize(serviceName string) {
	if !IsEnabled() || logsWriterInstance != nil {
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
	if !IsEnabled() || logsWriterInstance == nil {
		return
	}

	logsWriterInstance.stop()
	logsWriterInstance = nil
}

// WriteLog writes a log entry with the given message and tags.
func WriteLog(testID uint64, moduleName string, suiteName string, testName string, message string, tags string) {
	if !IsEnabled() || logsWriterInstance == nil {
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
