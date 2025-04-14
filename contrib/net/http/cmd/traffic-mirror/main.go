// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"net/http"
	"os"
	"runtime/debug"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	internal "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func getTracerVersion() string {
	info, _ := debug.ReadBuildInfo()
	for _, dep := range info.Deps {
		if dep.Path == "github.com/DataDog/dd-trace-go/v2" {
			return dep.Version
		}
	}

	return ""
}

type Config struct {
	ListenAddr      string
	HealthCheckAddr string
}

var defaultEnv = map[string]string{
	"DD_APPSEC_ENABLED":               "true",
	"DD_APPSEC_WAF_TIMEOUT":           "10ms", // increase the default timeout because we are not in-app
	"_DD_APPSEC_BLOCKING_UNAVAILABLE": "true", // Don't send blocking rules to this instance
	"DD_VERSION":                      getTracerVersion(),
	"DD_SERVICE":                      "traffic-mirror",

	// TODO: set once API Sec sampling works with AppSec standalone
	//"DD_APM_TRACING_ENABLED":          "false", // Disable APM tracing by default
}

func setEnv() {
	for k, v := range defaultEnv {
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}

func getConfig() Config {
	cfg := Config{
		ListenAddr:      os.Getenv("DD_TRAFFIC_MIRROR_LISTEN_ADDR"),
		HealthCheckAddr: os.Getenv("DD_TRAFFIC_MIRROR_HEALTHCHECK_ADDR"),
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
	logger := internal.Instrumentation.Logger()

	setEnv()
	config := getConfig()

	tracer.Start()
	defer tracer.Stop()

	if !internal.Instrumentation.AppSecEnabled() {
		logger.Error("Failed to enable appsec, stopping the server")
		os.Exit(1)
	}

	mux := httptrace.NewServeMux(
		httptrace.WithStatusCheck(func(statusCode int) bool { return false }), // Don't mark status codes as errors
		httptrace.NoDebugStack(),                                              // Don't do stacktrace that could be slow to generate
	)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// TODO: Support parsing the request body
		r.Body.Close()

		// Force the connection to be closed so we don't send a response
		wr, ok := w.(http.Hijacker)
		if !ok {
			logger.Error("ResponseWriter does not support Hijack")
			os.Exit(1)
		}

		conn, _, err := wr.Hijack()
		if err != nil {
			logger.Error("Failed to hijack connection: %v", err)
			os.Exit(1)
		}

		conn.Close()
	})

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

	logger.Info("Main traffic mirror server starting on address: %q", config.ListenAddr)
	if err := http.ListenAndServe(config.ListenAddr, mux); err != nil {
		logger.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}
