// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// chi v5 lives in its own file so both versions are imported as chi, without
// an alias.
import (
	"github.com/go-chi/chi/v5"
)

func serveV5() {
	r := chi.NewRouter()
	r.Get("/v5/router", ok)
	serve(r, "/v5/router")

	m := chi.NewMux()
	m.Get("/v5/mux", ok)
	serve(m, "/v5/mux")
}
