// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

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
func (l *Logger) Info(v ...interface{}) {
	l.SetPrefix("INFO: ")
	l.Println(v...)
}

// Infof logs an informational message with formatting
func (l *Logger) Infof(format string, v ...interface{}) {
	l.SetPrefix("INFO: ")
	l.Printf(format, v...)
}

// Warn logs a warning message
func (l *Logger) Warn(v ...interface{}) {
	l.SetPrefix("WARN: ")
	l.Println(v...)
}

// Warnf logs a warning message with formatting
func (l *Logger) Warnf(format string, v ...interface{}) {
	l.SetPrefix("WARN: ")
	l.Printf(format, v...)
}

// Error logs an error message
func (l *Logger) Error(v ...interface{}) {
	l.SetPrefix("ERROR: ")
	l.Println(v...)
}

// Errorf logs an error message with formatting
func (l *Logger) Errorf(format string, v ...interface{}) {
	l.SetPrefix("ERROR: ")
	l.Printf(format, v...)
}

// Debug logs a debug message
func (l *Logger) Debug(v ...interface{}) {
	l.SetPrefix("DEBUG: ")
	l.Println(v...)
}

// Debugf logs a debug message with formatting
func (l *Logger) Debugf(format string, v ...interface{}) {
	l.SetPrefix("DEBUG: ")
	l.Printf(format, v...)
}
