// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	ll "log"
	"os"
)

// Logger wraps the standard library log.Logger
type Logger struct {
	*ll.Logger
}

// NewLogger creates a new Logger instance
func NewLogger() *Logger {
	return &Logger{
		Logger: ll.New(os.Stdout, "", ll.LstdFlags),
	}
}

// Info logs an informational message
func (l *Logger) Info(format string, v ...interface{}) {
	l.SetPrefix("INFO: ")
	//l.Printf(format, v...)
	l.Printf(format, v...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, v ...interface{}) {
	l.SetPrefix("WARN: ")
	l.Printf(format, v...)
}

// Error logs an error message
func (l *Logger) Error(format string, v ...interface{}) {
	l.SetPrefix("ERROR: ")
	l.Printf(format, v...)
}

func (l *Logger) Errorf(format string, v ...interface{}) {
	l.Error("haproxy_spoa: "+format, v...)
}

// Debug logs a debug message
func (l *Logger) Debug(format string, v ...interface{}) {
	l.SetPrefix("DEBUG: ")
	l.Printf(format, v...)
}
