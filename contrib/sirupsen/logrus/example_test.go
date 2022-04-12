// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package logrus

import (
	"context"
	"os"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/sirupsen/logrus"
)

func ExampleHook() {
	// Setup logrus, do this once at the beginning of your program
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.AddHook(&DDContextLogHook{})

	// Setting output to Stdout so output can be validated
	logrus.SetOutput(os.Stdout)

	span, sctx := tracer.StartSpanFromContext(context.Background(), "mySpan")

	// Pass the current span context to the logger (Time is set for consistency in output here)
	cLog := logrus.WithContext(sctx).WithTime(time.Date(2000, 1, 1, 1, 1, 1, 0, time.UTC))
	// Log as desired using the context-aware logger
	cLog.Info("Completed some work!")

	span.Finish()
	// Output:
	// {"dd.span_id":0,"dd.trace_id":0,"level":"info","msg":"Completed some work!","time":"2000-01-01T01:01:01Z"}
}
