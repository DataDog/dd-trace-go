// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"fmt"
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
	transport *otlpTransport
	protocol  string // "http/json" or "http/protobuf"
	cfg       *internalconfig.Config
}

func newOTLPMetricsExporter(cfg *internalconfig.Config) *otlpMetricsExporter {
	return &otlpMetricsExporter{
		transport: newOTLPTransport(
			&http.Client{Timeout: cfg.AgentTimeout()},
			cfg.OTLPMetricsURL(),
			cfg.OTLPMetricsHeaders(),
		),
		protocol: cfg.OTLPMetricsProtocol(),
		cfg:      cfg,
	}
}

// export converts payload to an OTLP ExportMetricsServiceRequest and sends it.
func (e *otlpMetricsExporter) export(payload *pb.ClientStatsPayload) error {
	rms := buildOTLPMetricsRequest(payload, e.cfg)
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

	if sendErr := e.transport.send(body, contentType); sendErr != nil {
		log.Error("otlp_metrics_exporter: export to %s failed: %v", e.transport.endpoint, sendErr.Error())
		return sendErr
	}
	log.Debug("otlp_metrics_exporter: exported %d bytes (%s) to %s", len(body), e.protocol, e.transport.endpoint)
	return nil
}

// marshalExportRequestProto hand-encodes ExportMetricsServiceRequest (field 1: repeated ResourceMetrics)
// to avoid importing the collector package, which conflicts with some contrib modules via google.golang.org/genproto.
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
