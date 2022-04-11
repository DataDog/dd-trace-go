// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package logrus

import (
	"context"

	"github.com/sirupsen/logrus"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func ExampleHook() {
	//Setup logrus, do this once at the beginning of your program
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.AddHook(&DDContextLogHook{})

	span, sctx := tracer.StartSpanFromContext(context.Background(), "mySpan")

	//Pass the current context to the logger
	cLog := logrus.WithContext(sctx)
	//Log as desired using the context-aware logger
	cLog.Info("Completed some work!")

	span.Finish()
}
