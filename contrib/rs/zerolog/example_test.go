// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package zerolog_test

import (
	"context"
	"os"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/rs/zerolog"

	ddzerolog "github.com/DataDog/dd-trace-go/contrib/rs/zerolog"
)

func ExampleHook() {
	_ = tracer.Start()
	defer tracer.Stop()
	// Ensure your tracer is started and stopped
	// Setup zerolog, do this once at the beginning of your program
	zlog := zerolog.New(os.Stdout).
		Hook(&ddzerolog.DDContextLogHook{})

	span, sctx := tracer.StartSpanFromContext(context.Background(), "mySpan")
	defer span.Finish()

	// Pass the current span context to the logger (Time is set for consistency in output here)
	cLog := zlog.Info().Ctx(sctx).Time("time", time.Date(2000, 1, 1, 1, 1, 1, 0, time.UTC))
	// Log as desired using the context-aware logger
	cLog.Msg("Completed some work!")
	// You should see:
	// {"dd.span_id":0,"dd.trace_id":0,"level":"info","msg":"Completed some work!","time":"2000-01-01T01:01:01Z"}
}
