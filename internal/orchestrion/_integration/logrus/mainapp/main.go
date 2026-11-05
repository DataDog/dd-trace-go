// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp builds logrus loggers in package main, where only the otelc
// rules with target: main apply, and logs through each of them with a span in
// the context. TestLoggersInMain builds and runs it.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	logrustrace "github.com/DataDog/dd-trace-go/contrib/sirupsen/logrus/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func main() {
	contrib := logrus.New()
	contrib.AddHook(&logrustrace.DDContextLogHook{})

	loggers := []struct {
		name   string
		logger *logrus.Logger
	}{
		{"new", logrus.New()},
		{"standard", logrus.StandardLogger()},
		{"literal_ptr", &logrus.Logger{
			Hooks:    make(logrus.LevelHooks),
			ExitFunc: os.Exit,
		}},
		{"literal_val", valueLogger()},
		{"literal_no_hooks", &logrus.Logger{
			ExitFunc: os.Exit,
		}},
		{"alias", aliasedLogger()},
		{"contrib", contrib},
	}

	span, ctx := tracer.StartSpanFromContext(context.Background(), "mainapp.root")
	for _, l := range loggers {
		l.logger.SetOutput(os.Stdout)
		l.logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
		l.logger.SetLevel(logrus.InfoLevel)
		fmt.Printf("hooks %s %s\n", l.name, contextLogHookCounts(l.logger))
		l.logger.WithContext(ctx).WithField("logger", l.name).Info("correlated")
	}
	span.Finish()

	// The injected `defer tracer.Stop()` flushes the span, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func valueLogger() *logrus.Logger {
	l := logrus.Logger{
		Hooks:    make(logrus.LevelHooks),
		ExitFunc: os.Exit,
	}
	return &l
}

// contextLogHookCounts returns how many DDContextLogHook values each level
// fires, in logrus.AllLevels order.
func contextLogHookCounts(l *logrus.Logger) string {
	counts := make([]string, 0, len(logrus.AllLevels))
	for _, level := range logrus.AllLevels {
		var n int
		for _, h := range l.Hooks[level] {
			if _, ok := h.(*logrustrace.DDContextLogHook); ok {
				n++
			}
		}
		counts = append(counts, strconv.Itoa(n))
	}
	return strings.Join(counts, ",")
}
