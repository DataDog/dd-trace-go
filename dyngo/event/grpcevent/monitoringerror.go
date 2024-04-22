// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcevent

// MonitoringError is used to vehicle a gRPC error that also embeds a request
// status code
type MonitoringError struct {
	msg    string
	status uint32
}

// NewMonitoringError creates and returns a new gRPC monitoring error.
func NewMonitoringError(msg string, code uint32) error {
	return &MonitoringError{
		msg:    msg,
		status: code,
	}
}

// GRPCStatus returns the gRPC status code embedded in the error
func (e *MonitoringError) GRPCStatus() uint32 {
	return e.status
}

// Error implements the error interface
func (e *MonitoringError) Error() string {
	return e.msg
}
