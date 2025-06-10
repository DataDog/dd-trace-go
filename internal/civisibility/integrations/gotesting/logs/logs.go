// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

import (
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
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
	integrations.PushCiVisibilityCloseAction(func() {
		logsWriterInstance.stop()
	})
}

// WriteLog writes a log entry with the given message and tags.
func WriteLog(message string, tags string) {
	if logsWriterInstance == nil {
		return
	}

	logsWriterInstance.add(&logEntry{
		DdSource: "testoptimization",
		DdTags:   tags,
		Hostname: hostname.Get(),
		Message:  message,
		Service:  servName,
	})
}
