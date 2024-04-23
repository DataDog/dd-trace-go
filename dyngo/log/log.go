// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package log

type Logger interface {
	Errorf(format string, args ...any)
	Warnf(format string, args ...any)
	Infof(format string, args ...any)
	Debugf(format string, args ...any)
}

var logger Logger

// SetLogger replaces the current logger implementation with the provided one.
// The logger may be set to nil, which suppresses all logging output.
func SetLogger(l Logger) {
	logger = l
}

func Errorf(format string, args ...any) {
	if logger == nil {
		return
	}
	logger.Errorf("dyngo: "+format, args...)
}

func Warnf(format string, args ...any) {
	if logger == nil {
		return
	}
	logger.Warnf("dyngo: "+format, args...)
}

func Infof(format string, args ...any) {
	if logger == nil {
		return
	}
	logger.Infof("dyngo: "+format, args...)
}

func Debugf(format string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debugf("dyngo: "+format, args...)
}
