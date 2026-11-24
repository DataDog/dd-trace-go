// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otlpjson

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// OTLP AggregationTemporality values.
const (
	temporalityDelta      = 1
	temporalityCumulative = 2
)

// EncodeMetrics appends the OTLP/JSON ExportMetricsServiceRequest for rm to dst.
// A metric with an unknown aggregation or temporality is dropped from the
// request and reported in the returned error, as the upstream exporter does.
func EncodeMetrics(dst []byte, rm *metricdata.ResourceMetrics) ([]byte, error) {
	var errs []error
	e := encoder{buf: dst}
	e.beginObject()
	e.field("resourceMetrics")
	e.beginArray()
	e.elem()
	e.beginObject()
	e.resource(rm.Resource)
	e.field("scopeMetrics")
	e.beginArray()
	for _, sm := range rm.ScopeMetrics {
		e.elem()
		e.beginObject()
		e.scope(sm.Scope)
		e.field("metrics")
		e.beginArray()
		for _, m := range sm.Metrics {
			mark := len(e.buf)
			e.elem()
			if err := e.metric(m); err != nil {
				// Roll back the partially written metric and its separator.
				e.buf = e.buf[:mark]
				errs = append(errs, fmt.Errorf("metric %q: %w", m.Name, err))
			}
		}
		e.endArray()
		e.optStr("schemaUrl", sm.Scope.SchemaURL)
		e.endObject()
	}
	e.endArray()
	if rm.Resource != nil {
		e.optStr("schemaUrl", rm.Resource.SchemaURL())
	}
	e.endObject()
	e.endArray()
	e.endObject()
	return e.buf, joinErrors(errs)
}

func joinErrors(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	}
	return fmt.Errorf("%d metrics could not be encoded: %w", len(errs), errs[0])
}

func (e *encoder) metric(m metricdata.Metrics) error {
	e.beginObject()
	e.field("name")
	e.str(m.Name)
	e.optStr("description", m.Description)
	e.optStr("unit", m.Unit)
	var err error
	switch d := m.Data.(type) {
	case metricdata.Gauge[int64]:
		encodeGauge(e, d)
	case metricdata.Gauge[float64]:
		encodeGauge(e, d)
	case metricdata.Sum[int64]:
		err = encodeSum(e, d)
	case metricdata.Sum[float64]:
		err = encodeSum(e, d)
	case metricdata.Histogram[int64]:
		err = encodeHistogram(e, d)
	case metricdata.Histogram[float64]:
		err = encodeHistogram(e, d)
	case metricdata.ExponentialHistogram[int64]:
		err = encodeExponentialHistogram(e, d)
	case metricdata.ExponentialHistogram[float64]:
		err = encodeExponentialHistogram(e, d)
	case metricdata.Summary:
		encodeSummary(e, d)
	default:
		err = fmt.Errorf("unknown aggregation %T", m.Data)
	}
	e.endObject()
	return err
}

func temporalityValue(t metricdata.Temporality) (int64, error) {
	switch t {
	case metricdata.DeltaTemporality:
		return temporalityDelta, nil
	case metricdata.CumulativeTemporality:
		return temporalityCumulative, nil
	}
	return 0, fmt.Errorf("unknown temporality %s", t)
}

// dataPoints writes the "dataPoints" member, one element per point.
func dataPoints[P any](e *encoder, pts []P, point func(P)) {
	e.field("dataPoints")
	e.beginArray()
	for _, p := range pts {
		e.elem()
		point(p)
	}
	e.endArray()
}

func encodeGauge[N int64 | float64](e *encoder, g metricdata.Gauge[N]) {
	e.field("gauge")
	e.beginObject()
	dataPoints(e, g.DataPoints, func(p metricdata.DataPoint[N]) { numberPoint(e, p) })
	e.endObject()
}

func encodeSum[N int64 | float64](e *encoder, s metricdata.Sum[N]) error {
	t, err := temporalityValue(s.Temporality)
	if err != nil {
		return err
	}
	e.field("sum")
	e.beginObject()
	dataPoints(e, s.DataPoints, func(p metricdata.DataPoint[N]) { numberPoint(e, p) })
	e.field("aggregationTemporality")
	e.integer(t)
	if s.IsMonotonic {
		e.field("isMonotonic")
		e.boolean(true)
	}
	e.endObject()
	return nil
}

func encodeHistogram[N int64 | float64](e *encoder, h metricdata.Histogram[N]) error {
	t, err := temporalityValue(h.Temporality)
	if err != nil {
		return err
	}
	e.field("histogram")
	e.beginObject()
	dataPoints(e, h.DataPoints, func(p metricdata.HistogramDataPoint[N]) {
		e.beginObject()
		e.pointHeader(p.Attributes, p.StartTime.UnixNano(), p.Time.UnixNano())
		e.field("count")
		e.uint64Str(p.Count)
		e.field("sum")
		e.float(float64(p.Sum))
		if len(p.BucketCounts) > 0 {
			e.field("bucketCounts")
			e.beginArray()
			for _, c := range p.BucketCounts {
				e.elem()
				e.uint64Str(c)
			}
			e.endArray()
		}
		if len(p.Bounds) > 0 {
			e.field("explicitBounds")
			e.beginArray()
			for _, b := range p.Bounds {
				e.elem()
				e.float(b)
			}
			e.endArray()
		}
		exemplars(e, p.Exemplars)
		if v, ok := p.Min.Value(); ok {
			e.field("min")
			e.float(float64(v))
		}
		if v, ok := p.Max.Value(); ok {
			e.field("max")
			e.float(float64(v))
		}
		e.endObject()
	})
	e.field("aggregationTemporality")
	e.integer(t)
	e.endObject()
	return nil
}

func encodeExponentialHistogram[N int64 | float64](e *encoder, h metricdata.ExponentialHistogram[N]) error {
	t, err := temporalityValue(h.Temporality)
	if err != nil {
		return err
	}
	e.field("exponentialHistogram")
	e.beginObject()
	dataPoints(e, h.DataPoints, func(p metricdata.ExponentialHistogramDataPoint[N]) {
		e.beginObject()
		e.pointHeader(p.Attributes, p.StartTime.UnixNano(), p.Time.UnixNano())
		e.field("count")
		e.uint64Str(p.Count)
		e.field("sum")
		e.float(float64(p.Sum))
		e.field("scale")
		e.integer(int64(p.Scale))
		e.field("zeroCount")
		e.uint64Str(p.ZeroCount)
		e.expBuckets("positive", p.PositiveBucket)
		e.expBuckets("negative", p.NegativeBucket)
		exemplars(e, p.Exemplars)
		if v, ok := p.Min.Value(); ok {
			e.field("min")
			e.float(float64(v))
		}
		if v, ok := p.Max.Value(); ok {
			e.field("max")
			e.float(float64(v))
		}
		e.endObject()
	})
	e.field("aggregationTemporality")
	e.integer(t)
	e.endObject()
	return nil
}

func encodeSummary(e *encoder, s metricdata.Summary) {
	e.field("summary")
	e.beginObject()
	dataPoints(e, s.DataPoints, func(p metricdata.SummaryDataPoint) {
		e.beginObject()
		e.pointHeader(p.Attributes, p.StartTime.UnixNano(), p.Time.UnixNano())
		e.field("count")
		e.uint64Str(p.Count)
		e.field("sum")
		e.float(p.Sum)
		if len(p.QuantileValues) > 0 {
			e.field("quantileValues")
			e.beginArray()
			for _, q := range p.QuantileValues {
				e.elem()
				e.beginObject()
				e.field("quantile")
				e.float(q.Quantile)
				e.field("value")
				e.float(q.Value)
				e.endObject()
			}
			e.endArray()
		}
		e.endObject()
	})
	e.endObject()
}

// pointHeader writes the members every data point type starts with.
func (e *encoder) pointHeader(attrs attribute.Set, start, end int64) {
	e.attributeSet("attributes", attrs)
	e.unixNano("startTimeUnixNano", start)
	e.unixNano("timeUnixNano", end)
}

func (e *encoder) expBuckets(name string, b metricdata.ExponentialBucket) {
	if b.Offset == 0 && len(b.Counts) == 0 {
		return
	}
	e.field(name)
	e.beginObject()
	if b.Offset != 0 {
		e.field("offset")
		e.integer(int64(b.Offset))
	}
	if len(b.Counts) > 0 {
		e.field("bucketCounts")
		e.beginArray()
		for _, c := range b.Counts {
			e.elem()
			e.uint64Str(c)
		}
		e.endArray()
	}
	e.endObject()
}

func numberPoint[N int64 | float64](e *encoder, p metricdata.DataPoint[N]) {
	e.beginObject()
	e.pointHeader(p.Attributes, p.StartTime.UnixNano(), p.Time.UnixNano())
	switch v := any(p.Value).(type) {
	case int64:
		e.field("asInt")
		e.int64Str(v)
	case float64:
		e.field("asDouble")
		e.float(v)
	}
	exemplars(e, p.Exemplars)
	e.endObject()
}

func exemplars[N int64 | float64](e *encoder, exs []metricdata.Exemplar[N]) {
	if len(exs) == 0 {
		return
	}
	e.field("exemplars")
	e.beginArray()
	for _, ex := range exs {
		e.elem()
		e.beginObject()
		e.attributeSlice("filteredAttributes", ex.FilteredAttributes)
		e.unixNano("timeUnixNano", ex.Time.UnixNano())
		switch v := any(ex.Value).(type) {
		case int64:
			e.field("asInt")
			e.int64Str(v)
		case float64:
			e.field("asDouble")
			e.float(v)
		}
		if len(ex.SpanID) > 0 {
			e.field("spanId")
			e.hexID(ex.SpanID)
		}
		if len(ex.TraceID) > 0 {
			e.field("traceId")
			e.hexID(ex.TraceID)
		}
		e.endObject()
	}
	e.endArray()
}
