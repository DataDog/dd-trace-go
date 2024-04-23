// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package log

import (
	"github.com/datadog/dd-trace-go/dyngo/log"
)

type dyngoAdapter struct{}

func init() {
	log.SetLogger(dyngoAdapter{})
}

func (dyngoAdapter) Errorf(format string, args ...any) {
	Error(format, args...)
}
func (dyngoAdapter) Warnf(format string, args ...any) {
	Warn(format, args...)
}
func (dyngoAdapter) Infof(format string, args ...any) {
	Info(format, args...)
}
func (dyngoAdapter) Debugf(format string, args ...any) {
	Debug(format, args...)
}
