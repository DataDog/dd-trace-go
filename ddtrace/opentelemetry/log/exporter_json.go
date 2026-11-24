// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package log

import (
	"context"
	"errors"
	"net/url"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry/internal/otlpjson"
	"github.com/DataDog/dd-trace-go/v2/internal/config"

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

var errShutdown = errors.New("OTLP/JSON logs exporter is shut down")

// jsonExporter is the sdklog.Exporter used when the logs protocol is "http/json".
// The upstream otlploghttp exporter only produces protobuf, so this one encodes
// the SDK records to OTLP/JSON itself (see package otlpjson).
type jsonExporter struct {
	sender  *otlpjson.Sender
	stopped atomic.Bool
}

var _ sdklog.Exporter = (*jsonExporter)(nil)

// newJSONExporter creates an OTLP/JSON logs exporter from the logs configuration.
func newJSONExporter() *jsonExporter {
	cfg := config.Get()
	return &jsonExporter{sender: otlpjson.NewSender(otlpjson.SenderConfig{
		URL:           jsonExporterURL(cfg),
		Headers:       cfg.OTLPLogsHeaders(),
		Timeout:       cfg.OTLPLogsTimeout(),
		Gzip:          cfg.OTLPLogsCompression() == "gzip",
		Signal:        "logs",
		RejectedField: "rejectedLogRecords",
	})}
}

// jsonExporterURL resolves the endpoint like the HTTP protobuf exporter does:
// the configured logs URL, else the one derived from the agent configuration.
func jsonExporterURL(cfg *config.Config) string {
	if raw := cfg.OTLPLogsHTTPURL(); raw != "" {
		warnDoubledLogsPath(cfg)
		u, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		// Same as the upstream exporter: TLS only for https, plain http for any
		// other scheme (the endpoint may be a grpc:// URL shared with other signals),
		// and the decoded path is the one that is sent.
		if u.Scheme != "" && u.Scheme != "https" {
			u.Scheme = "http"
		}
		u.RawPath = ""
		return u.String()
	}
	endpoint, path, insecure := resolveOTLPEndpointHTTP()
	scheme := "https"
	if insecure {
		scheme = "http"
	}
	return scheme + "://" + endpoint + path
}

// Export implements sdklog.Exporter.
func (e *jsonExporter) Export(ctx context.Context, records []sdklog.Record) error {
	if e.stopped.Load() {
		return errShutdown
	}
	if len(records) == 0 {
		return nil
	}
	buf := otlpjson.GetBuffer()
	defer otlpjson.PutBuffer(buf)
	*buf = otlpjson.EncodeLogs(*buf, records)
	return e.sender.Send(ctx, *buf)
}

// Shutdown implements sdklog.Exporter. Later Export calls return errShutdown.
func (e *jsonExporter) Shutdown(context.Context) error {
	e.stopped.Store(true)
	e.sender.Close()
	return nil
}

// ForceFlush implements sdklog.Exporter. The exporter keeps no state to flush.
func (e *jsonExporter) ForceFlush(context.Context) error { return nil }
