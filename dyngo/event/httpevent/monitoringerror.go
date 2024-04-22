// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpevent

// MonitoringError is used to represent an HTTP error; usually resurfaced
// through AppSec SDKs.
type MonitoringError struct {
	message string
}

var _ error = (*MonitoringError)(nil)

// NewMonitoringError creates a new MonitoringError with the provided message.
func NewMonitoringError(message string) *MonitoringError {
	return &MonitoringError{message: message}
}

func (err *MonitoringError) Error() string {
	return err.message
}
