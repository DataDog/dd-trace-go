// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gatewayapi

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"

	"k8s.io/utils/ptr"
)

const (
	framework = "k8s.io/gateway-api"
)

var (
	instr  = instrumentation.Load(instrumentation.PackageK8SGatewayAPI)
	logger = instr.Logger()

	firstRequest sync.Once
)

// Config holds the configuration for the request mirror server.
type Config struct {
	httptrace.ServeConfig
	// Hijack is a flag to indicate if the server should hijack the connection and close it before sending a response to the client.
	// This is useful to reduce the number of open connections and avoid sending a response that will be ignored anyway.
	Hijack *bool
}

// HTTPRequestMirrorHandler is the handler for the request mirror server.
// It is made to receive requests from proxies supporting the request mirror feature from the k8s gateway API specification.
// It will parse the request body and send it to the WAF for analysis.
// The resulting [http.Handler] should not to be registered with the [httptrace.ServeMux] but instead with a standard [http.ServeMux].
func HTTPRequestMirrorHandler(config Config) http.Handler {
	if config.ServeConfig.SpanOpts == nil {
		config.ServeConfig.SpanOpts = []tracer.StartSpanOption{
			tracer.Tag(ext.SpanKind, ext.SpanKindServer),
			tracer.Tag(ext.Component, "k8s.io/gateway-api"),
		}
	}

	if config.Hijack == nil {
		config.Hijack = ptr.To[bool](true)
	}

	bodyProcessingMaxBytes := proxy.DefaultBodyParsingSizeLimit
	processor := proxy.NewProcessor(proxy.ProcessorConfig{
		Context:              context.Background(),
		BlockingUnavailable:  true,
		BodyParsingSizeLimit: &bodyProcessingMaxBytes,
		Framework:            framework,
		ContinueMessageFunc:  func(_ context.Context, _ proxy.ContinueActionOptions) error { return nil },
		BlockMessageFunc:     func(_ context.Context, _ proxy.BlockActionOptions) error { return nil },
	}, instr)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if config.Hijack != nil && *config.Hijack {
			defer hijackConnection(w).Close()
		}

		if strings.HasSuffix(r.Host, "-shadow") && r.Header.Get("X-Envoy-Internal") != "" {
			// Remove the -shadow suffix from the host when envoy is the one sending the request
			r.Host = strings.TrimSuffix(r.Host, "-shadow")
		}

		reqState, err := processor.OnRequestHeaders(r.Context(), requestHeader{r, config.SpanOpts})
		if err != nil {
			logger.Error("Failed to process request headers: %v", err)
			return
		}

		defer reqState.Close()

		body, err := io.ReadAll(io.LimitReader(r.Body, int64(bodyProcessingMaxBytes+1)))
		if err := processor.OnRequestBody(requestBody{body: body}, &reqState); err != nil {
			logger.Error("Failed to process request body: %v", err)
			return
		}
	})
}

// hijackConnection hijacks the connection from the http.ResponseWriter if possible. Panics otherwise.
func hijackConnection(w http.ResponseWriter) net.Conn {
	wr, ok := w.(http.Hijacker)
	if !ok {
		panic(fmt.Errorf("%T does not support http.Hijacker interface", w))
	}

	conn, _, err := wr.Hijack()
	if err != nil {
		panic(fmt.Errorf("failed to hijack connection: %s", err.Error()))
	}

	return conn
}
