// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp builds zerolog loggers in package main, where only the otelc
// rules with target: main apply, and logs through each of them with a span in
// the context. TestLoggersInMain builds and runs it.
package main

import (
	"context"
	"fmt"
	"os"
	"reflect"

	"example.com/zerologdep"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	zerologtrace "github.com/DataDog/dd-trace-go/contrib/rs/zerolog/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var pkgLogger = zerolog.New(os.Stdout)

func main() {
	loggers := []struct {
		name   string
		logger zerolog.Logger
	}{
		{"new", zerolog.New(os.Stdout)},
		{"package_var", pkgLogger},
		{"alias", aliasedLogger()},
		{"output", zerolog.New(os.Stderr).Output(os.Stdout)},
		{"global", log.Output(os.Stdout)},
		{"contrib", zerolog.New(os.Stdout).Hook(zerologtrace.DDContextLogHook{})},
		{"dependency", zerologdep.NewLogger(os.Stdout)},
	}

	span, ctx := tracer.StartSpanFromContext(context.Background(), "mainapp.root")
	for _, l := range loggers {
		fmt.Printf("hooks %s %d\n", l.name, contextLogHooks(l.logger))
		l.logger.Info().Ctx(ctx).Str("logger", l.name).Msg("correlated")
	}
	span.Finish()

	// The injected `defer tracer.Stop()` flushes the span, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

// contextLogHooks returns how many DDContextLogHook values l holds, read
// through reflection because zerolog does not export its hooks.
func contextLogHooks(l zerolog.Logger) int {
	hooks := reflect.ValueOf(l).FieldByName("hooks")
	var n int
	for i := range hooks.Len() {
		h := hooks.Index(i).Elem().Type()
		if h.Kind() == reflect.Pointer {
			h = h.Elem()
		}
		if h.Name() == "DDContextLogHook" {
			n++
		}
	}
	return n
}
