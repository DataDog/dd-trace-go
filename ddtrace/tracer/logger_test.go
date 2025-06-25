// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer_test

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"github.com/stretchr/testify/assert"
)

type logRecord struct {
	level tracer.LogLevel
	msg   string
}

func TestAdaptLogger(t *testing.T) {
	recorder := make([]logRecord, 0)
	l := tracer.AdaptLogger(func(lvl tracer.LogLevel, msg string, _ ...any) {
		recorder = append(recorder, logRecord{
			level: lvl,
			msg:   msg,
		})
	})
	defer log.UseLogger(l)()

	log.Warn("warning\n")
	log.Info("info\n")

	assertions := assert.New(t)
	assertions.Equal(2, len(recorder))
	assertions.Equal(log.LevelWarn, recorder[0].level)
	assertions.Contains(recorder[0].msg, "WARN: warning")
	assertions.Equal(log.LevelInfo, recorder[1].level)
	assertions.Contains(recorder[1].msg, "INFO: info")
}
