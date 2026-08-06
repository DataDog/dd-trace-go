// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules that reproduce
// contrib/go-chi/chi's orchestrion.yml. The single aspect there wraps
// chi.NewMux/chi.NewRouter call sites with chitrace.Middleware() directly, so
// no hook functions are needed here.
//
// This package exists only so otelc.yaml has a directory to live in, and so
// that a build blank-importing it (via otelc/all) also requires
// contrib/go-chi/chi/v2, which the wrap_call rule injects into the target
// application's own source.
package otelc

import (
	_ "github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2" // chitrace, referenced by otelc.yaml
)
