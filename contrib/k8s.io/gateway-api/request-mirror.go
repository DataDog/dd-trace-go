// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gatewayapi

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

const (
	maxBodyBytes = 5 * 1 << 20 // 5 MB
	maxNbHeaders = 1_000

	RequestMirrorLabelKey   = "app"
	RequestMirrorLabelValue = "request-mirror"
)

var (
	instr  = instrumentation.Load(instrumentation.PackageK8SGatewayAPI)
	logger = instr.Logger()

	logFirstRequest sync.Once
)

// Config holds the configuration for the request mirror server.
type Config struct {
	httptrace.ServeConfig
}

// HTTPRequestMirrorHandler is the handler for the request mirror server.
// It is made to receive requests from proxies supporting the request mirror feature from the k8s gateway API specification.
func HTTPRequestMirrorHandler(config Config) http.Handler {
	if config.ServeConfig.SpanOpts == nil {
		config.ServeConfig.SpanOpts = []tracer.StartSpanOption{
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logFirstRequest.Do(func() {
			logger.Info("contrib/k8s.io/gateway-api: Configuring request mirror server: %#v", config)
		})

		if strings.HasSuffix(r.Host, "-shadow") && r.Header.Get("X-Envoy-Internal") != "" {
			// Remove the -shadow suffix from the host when envoy is the one sending the request
			r.Host = strings.TrimSuffix(r.Host, "-shadow")
		}

		config := config
		if config.Resource == "" {
			config.Resource = r.Method + " " + path.Clean(r.URL.Path)
		}

		_, _, afterHandle, blocked := httptrace.BeforeHandle(&config.ServeConfig, w, r)

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
