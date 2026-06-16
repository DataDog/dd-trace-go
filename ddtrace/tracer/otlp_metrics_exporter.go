// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	otlpmetrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	otlpMetricsContentTypeJSON  = "application/json"
	otlpMetricsContentTypeProto = "application/x-protobuf"
)

// otlpMetricsExporter converts ClientStatsPayload to OTLP metrics and sends them over HTTP.
type otlpMetricsExporter struct {
	client   *http.Client
	url      string
	headers  map[string]string
	protocol string // "http/json" or "http/protobuf"
	cfg      *internalconfig.Config
}

func newOTLPMetricsExporter(cfg *internalconfig.Config) *otlpMetricsExporter {
	return &otlpMetricsExporter{
		client:   &http.Client{Timeout: cfg.AgentTimeout()},
		url:      cfg.OTLPMetricsURL(),
		headers:  cfg.OTLPMetricsHeaders(),
		protocol: cfg.OTLPMetricsProtocol(),
		cfg:      cfg,
	}
}

// export converts payload to an OTLP ExportMetricsServiceRequest and sends it.
// A nil or empty payload produces no request. Errors are logged and returned.
func (e *otlpMetricsExporter) export(payload *pb.ClientStatsPayload) error {
	rms := BuildOTLPMetricsRequest(payload, e.cfg)
	if len(rms) == 0 {
		return nil
	}

	var body []byte
	var contentType string
	var err error

	if e.protocol == "http/json" {
		body, err = marshalExportRequestJSON(rms)
		contentType = otlpMetricsContentTypeJSON
	} else {
		body, err = marshalExportRequestProto(rms)
		contentType = otlpMetricsContentTypeProto
	}
	if err != nil {
		return fmt.Errorf("otlp_metrics_exporter: marshal failed: %w", err)
	}

	if sendErr := e.send(body, contentType); sendErr != nil {
		log.Error("otlp_metrics_exporter: export to %s failed: %v", e.url, sendErr.Error())
		return sendErr
	}
	log.Debug("otlp_metrics_exporter: exported %d bytes (%s) to %s", len(body), e.protocol, e.url)
	return nil
}

// marshalExportRequestProto encodes a ResourceMetrics slice as the protobuf binary of
// ExportMetricsServiceRequest (field 1: repeated ResourceMetrics). This avoids importing
// the collector package which transitively pulls in grpc-gateway and conflicts with
// older monolithic google.golang.org/genproto in some contrib modules.
func marshalExportRequestProto(rms []*otlpmetrics.ResourceMetrics) ([]byte, error) {
	var buf []byte
	for _, rm := range rms {
		b, err := proto.Marshal(rm)
		if err != nil {
			return nil, err
		}
		buf = protowire.AppendTag(buf, 1, protowire.BytesType)
		buf = protowire.AppendBytes(buf, b)
	}
	return buf, nil
}

// marshalExportRequestJSON encodes a ResourceMetrics slice as the JSON of
// ExportMetricsServiceRequest: {"resourceMetrics": [...]}.
func marshalExportRequestJSON(rms []*otlpmetrics.ResourceMetrics) ([]byte, error) {
	type exportReq struct {
		ResourceMetrics []json.RawMessage `json:"resourceMetrics"`
	}
	items := make([]json.RawMessage, 0, len(rms))
	for _, rm := range rms {
		b, err := protojson.Marshal(rm)
		if err != nil {
			return nil, err
		}
		items = append(items, json.RawMessage(b))
	}
	return json.Marshal(exportReq{ResourceMetrics: items})
}

func (e *otlpMetricsExporter) send(data []byte, contentType string) error {
	req, err := http.NewRequest(http.MethodPost, e.url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("cannot create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return nil
}
