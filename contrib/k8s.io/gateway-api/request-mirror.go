// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gatewayapi

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"k8s.io/utils/ptr"
)

const (
	maxBodyBytes = 5 * 1 << 20 // 5 MB
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

var requestsCount atomic.Uint32

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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if config.Hijack != nil && *config.Hijack {
			defer hijackConnection(w).Close()
		}

		requestsCount.Add(1)

		firstRequest.Do(func() {
			logger.Info("contrib/k8s.io/gateway-api: Configuring request mirror server: %#v", config)
			go func() {
				ticker := time.NewTicker(time.Minute)
				for {
					<-ticker.C
					logger.Info("contrib/k8s.io/gateway-api: Analyzed %d requests last minute", requestsCount.Swap(0))
				}
			}()
		})

		if strings.HasSuffix(r.Host, "-shadow") && r.Header.Get("X-Envoy-Internal") != "" {
			// Remove the -shadow suffix from the host when envoy is the one sending the request
			r.Host = strings.TrimSuffix(r.Host, "-shadow")
		}

		config := config
		if config.Resource == "" {
			config.Resource = r.Method + " " + path.Clean(r.URL.Path)
		}

		_, r, _, _ = httptrace.BeforeHandle(&config.ServeConfig, w, r)
		span, ok := tracer.SpanFromContext(r.Context())
		if !ok {
			logger.Error("No span found in request context")
			return
		}

		// We have to manually finish the span to workaround the default behaviour of gather data from the http.ResponseWriter
		// we would make span inaccurate in this case
		defer span.Finish(config.FinishOpts...)

		op, ok := dyngo.FindOperation[httpsec.HandlerOperation](r.Context())
		if !ok {
			logger.Error("No operation found in request context")
			return
		}

		// Same here to bypass default behaviour of gather data from the http.ResponseWriter and send it to the WAF
		defer op.Finish(httpsec.HandlerOperationRes{})

		analyzeRequestBody(r)
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

// parseBody parses the request body based on the list of content types provided in the Content-Type header.
// It returns the parsed body as an interface{} or an error if parsing fails.
func parseBody(contentType string, reader io.Reader) (any, error) {
	for _, typ := range strings.Split(contentType, ",") {
		if typ == "" {
			continue
		}

		mimeType, _, err := mime.ParseMediaType(typ)
		if err != nil {
			logger.Debug("Failed to parse content type: %s", err.Error())
			continue
		}

		var body any
		switch {
		// Handle cases like application/vnd.api+json: https://jsonapi.org/
		case mimeType == "application/json" || strings.HasSuffix(mimeType, "+json"):
			err = json.NewDecoder(reader).Decode(&body)
		}

		if body == nil {
			continue
		}

		if err != nil {
			logger.Debug("Failed to decode body using content-type %q: %s", mimeType, err.Error())
			continue
		}

		return body, nil
	}

	return nil, fmt.Errorf("unsupported content type: %s", contentType)
}

// analyzeRequestBody check if the body can be parsed and if so, parse it and send it to the WAF
// and return if blocking was performed on the http.ResponseWriter
func analyzeRequestBody(r *http.Request) bool {
	if r.Body == nil {
		logger.Debug("Request body is nil")
		return false
	}

	body, err := parseBody(r.Header.Get("Content-Type"), io.LimitReader(r.Body, maxBodyBytes))

	if err == io.EOF {
		logger.Debug("Request body was too large to be parsed")
		return false
	}

	if err != nil {
		logger.Debug("Failed to parse request body: %s", err.Error())
		return false
	}

	if body == nil {
		return false
	}

	return appsec.MonitorParsedHTTPBody(r.Context(), body) != nil
}
