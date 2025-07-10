// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"net/http"
	"os"

	gatewayapi "github.com/DataDog/dd-trace-go/contrib/k8s.io/gateway-api/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

var (
	instr  = instrumentation.Load(instrumentation.PackageK8SGatewayAPI)
	logger = instr.Logger()
)

type Config struct {
	ListenAddr      string
	HealthCheckAddr string
}

func getConfig() Config {
	cfg := Config{
		ListenAddr:      env.Getenv("DD_REQUEST_MIRROR_LISTEN_ADDR"),
		HealthCheckAddr: env.Getenv("DD_REQUEST_MIRROR_HEALTHCHECK_ADDR"),
	}

	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}

	if cfg.HealthCheckAddr == "" {
		cfg.HealthCheckAddr = ":8081"
	}

	return cfg
}

func main() {
	config := getConfig()

	if err := tracer.Start(tracer.WithServiceVersion(instrumentation.Version())); err != nil {
		logger.Error("Failed to start tracer: %s", err.Error())
		os.Exit(1)
	}

	defer tracer.Stop()

	if !instr.AppSecEnabled() {
		logger.Error("Failed to enable appsec, stopping the server")
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/", gatewayapi.HTTPRequestMirrorHandler(gatewayapi.Config{
		ServeConfig: httptrace.ServeConfig{
			Framework: "k8s.io/gateway-api",
			FinishOpts: []tracer.FinishOption{
				tracer.NoDebugStack(),
			},
			SpanOpts: []tracer.StartSpanOption{
				tracer.Tag(ext.SpanKind, ext.SpanKindServer),
				tracer.Tag(ext.Component, "k8s.io/gateway-api"),
			},
		},
	}))

	go func() {
		healthcheckMux := http.NewServeMux()
		healthcheckMux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": "ok"}`))
		})

		logger.Info("Starting request mirror health check server on address: %q", config.HealthCheckAddr)
		if err := http.ListenAndServe(config.HealthCheckAddr, healthcheckMux); err != nil {
			logger.Error("Failed to start health check server", "error", err)
			os.Exit(1)
		}
	}()

	logger.Info("Main request mirror server starting on address: %q", config.ListenAddr)
	if err := http.ListenAndServe(config.ListenAddr, mux); err != nil {
		logger.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}
