// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otlpjson

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

// EncodeLogs appends the OTLP/JSON ExportLogsServiceRequest for records to dst.
// Records are grouped by resource, then by instrumentation scope.
func EncodeLogs(dst []byte, records []sdklog.Record) []byte {
	type scopeGroup struct {
		scope   instrumentation.Scope
		records []int
	}
	type resourceGroup struct {
		res    *resource.Resource
		scopes []scopeGroup
	}
	// A batch almost always has one resource and a handful of scopes, so a
	// linear scan is cheaper than maps.
	var groups []resourceGroup
	for i := range records {
		res, scope := records[i].Resource(), records[i].InstrumentationScope()
		gi := -1
		for j := range groups {
			if groups[j].res == res || groups[j].res.Equivalent() == res.Equivalent() {
				gi = j
				break
			}
		}
		if gi < 0 {
			groups = append(groups, resourceGroup{res: res})
			gi = len(groups) - 1
		}
		g := &groups[gi]
		si := -1
		for j := range g.scopes {
			if g.scopes[j].scope == scope {
				si = j
				break
			}
		}
		if si < 0 {
			g.scopes = append(g.scopes, scopeGroup{scope: scope})
			si = len(g.scopes) - 1
		}
		g.scopes[si].records = append(g.scopes[si].records, i)
	}

	e := encoder{buf: dst}
	e.beginObject()
	e.field("resourceLogs")
	e.beginArray()
	for _, g := range groups {
		e.elem()
		e.beginObject()
		e.resource(g.res)
		e.field("scopeLogs")
		e.beginArray()
		for _, sg := range g.scopes {
			e.elem()
			e.beginObject()
			e.scope(sg.scope)
			e.field("logRecords")
			e.beginArray()
			for _, idx := range sg.records {
				e.elem()
				e.logRecord(&records[idx])
			}
			e.endArray()
			e.optStr("schemaUrl", sg.scope.SchemaURL)
			e.endObject()
		}
		e.endArray()
		e.optStr("schemaUrl", g.res.SchemaURL())
		e.endObject()
	}
	e.endArray()
	e.endObject()
	return e.buf
}

func (e *encoder) logRecord(r *sdklog.Record) {
	e.beginObject()
	e.unixNano("timeUnixNano", r.Timestamp().UnixNano())
	e.unixNano("observedTimeUnixNano", r.ObservedTimestamp().UnixNano())
	// log.Severity values are the OTLP SeverityNumber values.
	if s := r.Severity(); s != 0 {
		e.field("severityNumber")
		e.integer(int64(s))
	}
	e.optStr("severityText", r.SeverityText())
	if body := r.Body(); body.Type() != attribute.EMPTY {
		e.field("body")
		e.anyValue(body)
	}
	if r.AttributesLen() > 0 {
		e.field("attributes")
		e.beginArray()
		r.WalkAttributes(func(kv attribute.KeyValue) bool {
			e.elem()
			e.keyValue(kv)
			return true
		})
		e.endArray()
	}
	if f := r.TraceFlags(); f != 0 {
		e.field("flags")
		e.integer(int64(f))
	}
	if tid := r.TraceID(); tid.IsValid() {
		e.field("traceId")
		e.hexID(tid[:])
	}
	if sid := r.SpanID(); sid.IsValid() {
		e.field("spanId")
		e.hexID(sid[:])
	}
	e.optStr("eventName", r.EventName())
	e.endObject()
}
