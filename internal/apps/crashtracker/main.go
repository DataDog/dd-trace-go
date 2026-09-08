// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// crashtracker is a test app that deliberately crashes on request, so the
// nightly test-apps run can confirm crash reports keep reaching Error
// Tracking end to end against a real Agent and a real site.
package main

import (
	"log"
	"net/http"

	"github.com/DataDog/dd-trace-go/internal/apps/v2"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/v2/crashtracker"
)

func main() {
	// Call Start before RunHTTP: RunHTTP starts the tracer, and crashtracker's
	// own resolveService falls back to globalconfig's service name when the
	// tracer set one first, so starting crashtracker after the tracer would
	// pick up the right service anyway -- but the documented lifecycle
	// (doc.go) is "as early as possible, before any goroutines are created",
	// and this app should demonstrate that contract, not just a shape that
	// happens to still work.
	if err := crashtracker.Start(); err != nil {
		log.Printf("crashtracker.Start: %v", err)
	}

	app := apps.Config{}
	app.RunHTTP(func() http.Handler {
		mux := httptrace.NewServeMux()
		mux.HandleFunc("/crash", func(w http.ResponseWriter, _ *http.Request) {
			// net/http recovers a panic raised directly in a handler
			// goroutine, so it would never reach the Go runtime's
			// fatal-crash path; panicking in a separate goroutine bypasses
			// that recovery and produces the genuine, unrecovered crash
			// this scenario exists to report.
			go panic("internal/apps/crashtracker: deliberate crash for the nightly test-apps scenario")
			w.WriteHeader(http.StatusAccepted)
		})
		return mux
	})
}
