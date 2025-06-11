// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

import (
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
)

// Initialize initializes the logs writer for CI visibility.
func Initialize(serviceName string) {
	if logsWriterInstance != nil {
		return
	}

	servName = serviceName
	logsWriterInstance = newLogsWriter()
}

// Stop stops the logs writer and cleans up resources.
func Stop() {
	if logsWriterInstance == nil {
		return
	}

	logsWriterInstance.stop()
	logsWriterInstance = nil
}

// WriteLog writes a log entry with the given message and tags.
func WriteLog(testID uint64, moduleName string, suiteName string, testName string, message string, tags string) {
	if logsWriterInstance == nil {
		return
	}

	testIDStr := strconv.FormatUint(testID, 10)
	hname := hostname.Get()
	if hname == "" {
		hname, _ = os.Hostname()
	}
	logsWriterInstance.add(&logEntry{
		DdSource:   "testoptimization",
		Hostname:   hname,
		Timestamp:  time.Now().UnixMilli(),
		Message:    message,
		DdTraceId:  testIDStr,
		DdSpanId:   testIDStr,
		TestModule: moduleName,
		TestSuite:  suiteName,
		TestName:   testName,
		Service:    servName,
		DdTags:     tags,
	})
}
