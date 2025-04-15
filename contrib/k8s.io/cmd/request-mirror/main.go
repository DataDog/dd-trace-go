// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"runtime/debug"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

const (
	maxBodyBytes = 5 * 1 << 20 // 5 MB
	maxNbHeaders = 1_000
)

var (
	instr  = instrumentation.Load(instrumentation.PackageK8SGatewayAPI)
	logger = instr.Logger()
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
	"DD_SERVICE":                      "request-mirror",
	"DD_APM_TRACING_ENABLED":          "false", // Disable APM tracing by default
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
		ListenAddr:      os.Getenv("DD_REQUEST_MIRROR_LISTEN_ADDR"),
		HealthCheckAddr: os.Getenv("DD_REQUEST_MIRROR_HEALTHCHECK_ADDR"),
	}

	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}

	if cfg.HealthCheckAddr == "" {
		cfg.HealthCheckAddr = ":8081"
	}

	return cfg
}

// analyzeRequestBody check if the body can be parsed and if so, parse it and send it to the WAF
// and return if blocking was performed on the http.ResponseWriter
func analyzeRequestBody(r *http.Request) bool {
	if r.Body == nil {
		logger.Debug("Request body is nil")
		return false
	}

	if r.ContentLength == 0 {
		logger.Debug("Request body is empty")
		return false
	}

	var (
		body any
		err  error
	)

	// Check if the body is a valid JSON
	switch r.Header.Get("Content-Type") {
	case "application/json":
		body = make(map[string]any)
		err = json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&body)
	}

	if err == io.EOF {
		return false
	}

	if err != nil {
		logger.Debug("Failed to parse request body: %v", err)
		return false
	}

	if body == nil {
		return false
	}

	return appsec.MonitorParsedHTTPBody(r.Context(), body) != nil
}

func main() {
	setEnv()
	config := getConfig()

	if err := tracer.Start(); err != nil {
		logger.Error("Failed to start tracer: %v", err)
		os.Exit(1)
	}

	defer tracer.Stop()

	if !instr.AppSecEnabled() {
		logger.Error("Failed to enable appsec, stopping the server")
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header) > maxNbHeaders {
			logger.Info("More than 1000 headers in request, skipping it...")
			return
		}

		if strings.HasSuffix(r.Host, "-shadow") && r.Header.Get("X-Envoy-Internal") != "" {
			// Remove the -shadow suffix from the host when envoy is the one sending the request
			r.Host = strings.TrimSuffix(r.Host, "-shadow")
		}

		_, _, afterHandle, blocked := httptrace.BeforeHandle(&httptrace.ServeConfig{
			Framework: "sigs.k8s.io/gateway-api",
			Resource:  r.Method + " " + path.Clean(r.URL.Path),
			SpanOpts: []tracer.StartSpanOption{
				tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			},
			FinishOpts: []tracer.FinishOption{
				tracer.NoDebugStack(),
			},
		}, w, r)

		defer afterHandle()

		if blocked {
			logger.Error("Unexpected request blocking response was sent")
		}

		analyzeRequestBody(r)

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

		defer conn.Close()
	})

	go func() {
		healthcheckMux := http.NewServeMux()
		healthcheckMux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": "ok"}`))
		})

		logger.Info("Starting request mirror health check server on address: %q", config.HealthCheckAddr)
		if err := http.ListenAndServe(config.HealthCheckAddr, nil); err != nil {
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
