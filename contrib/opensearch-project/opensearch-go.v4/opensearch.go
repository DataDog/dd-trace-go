// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

// Package opensearch provides tracing functions for tracing the opensearch-project/opensearch-go/v4 package (https://github.com/opensearch-project/opensearch-go).
package opensearch

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var (
	instr *instrumentation.Instrumentation
	// bodyCutoff specifies the maximum number of bytes that will be stored as a tag
	// value obtained from an HTTP request or response body.
	bodyCutoff = 5 * 1024

	_ opensearchtransport.Interface    = (*transport)(nil)
	_ opensearchtransport.Discoverable = (*transport)(nil)
	_ opensearchtransport.Measurable   = (*transport)(nil)
	_ opensearch.Streamer              = (*transport)(nil)
	_ io.Closer                        = (*transport)(nil)
)

func init() {
	instr = instrumentation.Load(instrumentation.PackageOpenSearchProjectOpenSearchGoV4)
}

// TraceClient traces OpenSearch client. It replaces c.Transport in place, so it must not be
// called while other goroutines use c, such as a client created with DiscoverNodesOnStart.
// Use NewClient in that case.
func TraceClient(c *opensearch.Client, opts ...Option) {
	c.Transport = newTransport(c.Transport, newConfig(opts...))
}

// NewDefaultClient returns a new default opensearch.Client enhanced with tracing.
func NewDefaultClient(opts ...Option) (*opensearch.Client, error) {
	return NewClient(opensearch.Config{}, opts...)
}

// NewClient returns a new opensearch.Client enhanced with tracing.
func NewClient(cfg opensearch.Config, opts ...Option) (*opensearch.Client, error) {
	c, err := opensearch.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	// Startup discovery may already be reading c.Transport in another goroutine.
	// Wrap a copy so upstream's client remains unchanged while sharing its transport.
	traced := *c
	traced.Transport = newTransport(c.Transport, newConfig(opts...))
	return &traced, nil
}

type transport struct {
	origin opensearchtransport.Interface
	config *config
	// spanCfg holds the tags that are the same for every request made
	// through this transport.
	spanCfg *tracer.StartSpanConfig
}

func newTransport(origin opensearchtransport.Interface, cfg *config) *transport {
	return &transport{
		origin: origin,
		config: cfg,
		spanCfg: tracer.NewStartSpanConfig(
			instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource),
			tracer.SpanType(ext.SpanTypeOpenSearch),
			tracer.Tag(ext.Component, string(instrumentation.PackageOpenSearchProjectOpenSearchGoV4)),
			tracer.Tag(ext.SpanKind, ext.SpanKindClient),
			tracer.Tag(ext.DBSystem, ext.DBSystemOpenSearch),
		),
	}
}

// Perform traces the opensearch request.
func (t *transport) Perform(req *http.Request) (*http.Response, error) {
	return t.trace(req, t.origin.Perform, false)
}

// Stream traces a request without reading or closing the response body.
func (t *transport) Stream(req *http.Request) (*http.Response, error) {
	streamer, ok := t.origin.(opensearch.Streamer)
	if !ok {
		return nil, opensearch.ErrTransportMissingMethodStream
	}
	return t.trace(req, streamer.Stream, true)
}

func (t *transport) trace(req *http.Request, perform func(*http.Request) (*http.Response, error), streaming bool) (*http.Response, error) {
	opts := []tracer.StartSpanOption{
		tracer.WithTags(map[string]any{
			ext.ResourceName:     t.config.resourceNamer(req.URL.Path, req.Method),
			ext.OpenSearchMethod: req.Method,
			ext.OpenSearchURL:    req.URL.Path,
			ext.OpenSearchParams: req.URL.Query().Encode(),
		}),
		tracer.WithStartSpanConfig(t.spanCfg),
	}
	if t.config.customTags != nil {
		opts = append(opts, tracer.WithTags(t.config.customTags))
	}
	span, ctx := tracer.StartSpanFromContext(req.Context(), "opensearch.query", opts...)
	req = req.WithContext(ctx)
	contentEncoding := req.Header.Get("Content-Encoding")
	if !streaming {
		snip, rc, err := peek(req.Body, contentEncoding, int(req.ContentLength), bodyCutoff)
		if err == nil {
			span.SetTag(ext.OpenSearchBody, snip)
		}
		req.Body = rc
	}
	resp, err := perform(req)
	// The upstream transport selects the destination by updating the request URL.
	span.SetTag(ext.NetworkDestinationName, req.URL.Hostname())
	span.SetTag(ext.TargetHost, req.URL.Hostname())
	span.SetTag(ext.TargetPort, req.URL.Port())
	if resp != nil {
		span.SetTag(ext.HTTPCode, strconv.Itoa(resp.StatusCode))
	}
	if err != nil {
		span.Finish(tracer.WithError(err))
		return resp, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snip := http.StatusText(resp.StatusCode)
		if !streaming {
			body, rc, err := peek(resp.Body, contentEncoding, int(resp.ContentLength), bodyCutoff)
			if err == nil {
				snip = body
			}
			resp.Body = rc
		}
		span.Finish(tracer.WithError(errors.New(snip)))
		return resp, nil
	}
	span.Finish()
	return resp, nil
}

// Close releases the underlying transport's resources when supported.
func (t *transport) Close() error {
	if closer, ok := t.origin.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// DiscoverNodes implements the opensearchtransport.Discoverable interface.
func (t *transport) DiscoverNodes(ctx context.Context) error {
	if dt, ok := t.origin.(opensearchtransport.Discoverable); ok {
		return dt.DiscoverNodes(ctx)
	}
	return opensearch.ErrTransportMissingMethodDiscoverNodes
}

// Metrics implements the opensearchtransport.Measurable interface.
func (t *transport) Metrics() (opensearchtransport.Metrics, error) {
	if dt, ok := t.origin.(opensearchtransport.Measurable); ok {
		return dt.Metrics()
	}
	return opensearchtransport.Metrics{}, opensearch.ErrTransportMissingMethodMetrics
}

var (
	idRegexp         = regexp.MustCompile(`/([0-9]+)([/\?]|$)`)
	idPlaceholder    = []byte("/?$2")
	indexRegexp      = regexp.MustCompile("[0-9]{2,}")
	indexPlaceholder = []byte("?")
)

// quantize quantizes an OpenSearch to extract a meaningful resource from the request.
// We quantize based on the method+url with some cleanup applied to the URL.
// URLs with an ID will be generalized as will (potential) timestamped indices.
func quantize(url, method string) string {
	quantizedURL := idRegexp.ReplaceAll([]byte(url), idPlaceholder)
	quantizedURL = indexRegexp.ReplaceAll(quantizedURL, indexPlaceholder)
	return fmt.Sprintf("%s %s", method, quantizedURL)
}

// peek attempts to return the first n bytes, as a string, from the provided io.ReadCloser.
// It returns a new io.ReadCloser which points to the same underlying stream and can be read
// from to access the entire data including the snippet. max is used to specify the length
// of the stream contained in the reader. If unknown, it should be -1. If 0 < max < n it
// will override n.
func peek(rc io.ReadCloser, encoding string, maxLen, n int) (string, io.ReadCloser, error) {
	if rc == nil {
		return "", rc, errors.New("empty stream")
	}
	if maxLen > 0 && maxLen < n {
		n = maxLen
	}
	r := bufio.NewReaderSize(rc, n)
	rc2 := struct {
		io.Reader
		io.Closer
	}{
		Reader: r,
		Closer: rc,
	}
	snip, err := r.Peek(n)
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		return string(snip), rc2, err
	}
	if encoding == "gzip" {
		// unpack the snippet
		gzr, err2 := gzip.NewReader(bytes.NewReader(snip))
		if err2 != nil {
			// snip wasn't gzip; return it as is
			return string(snip), rc2, nil
		}
		defer gzr.Close()
		snip, err = io.ReadAll(gzr)
	}
	return string(snip), rc2, err
}
