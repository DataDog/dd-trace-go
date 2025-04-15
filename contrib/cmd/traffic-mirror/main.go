// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"net/http"
	"os"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var instr = instrumentation.Load(instrumentation.PackageNetHTTP)

func main() {
	logger := instr.Logger()

	config := getConfig()
	config.SetEnv()

	if err := tracer.Start(); err != nil {
		logger.Error("Failed to start tracer: %v", err)
		os.Exit(1)
	}

	defer tracer.Stop()

	if !instr.AppSecEnabled() {
		logger.Error("Failed to enable appsec, stopping the server")
		os.Exit(1)
	}

	go func() {
		healthcheckMux := http.NewServeMux()
		healthcheckMux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": "ok"}`))
		})

		logger.Info("Starting traffic mirror health check server on address: %q", config.HealthCheckAddr)
		if err := http.ListenAndServe(config.HealthCheckAddr, nil); err != nil {
			logger.Error("Failed to start health check server", "error", err)
			os.Exit(1)
		}
	}()

	mux := newServer(config)

	logger.Info("Main traffic mirror server starting on address: %q", config.ListenAddr)
	if err := http.ListenAndServe(config.ListenAddr, mux); err != nil {
		logger.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}
