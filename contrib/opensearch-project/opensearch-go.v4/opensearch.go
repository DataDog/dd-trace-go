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
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const (
	componentName      = "opensearch-project/opensearch-go/v4"
	defaultServiceName = "opensearch.client"
)

var (
	// bodyCutoff specifies the maximum number of bytes that will be stored as a tag
	// value obtained from an HTTP request or response body.
	bodyCutoff = 5 * 1024

	_ opensearchtransport.Interface    = (*transport)(nil)
	_ opensearchtransport.Discoverable = (*transport)(nil)
	_ http.RoundTripper                = (*roundTripper)(nil)
)

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/opensearch-project/opensearch-go/v4")
}

// WrapClient wraps OpenSearch client with tracing.
func WrapClient(c *opensearch.Client, opts ...Option) {
	tracerConfig := defaultConfig()
	for _, fn := range opts {
		fn(tracerConfig)
	}
	opensearchtransport := c.Transport
	t := &transport{
		opensearchtransport: opensearchtransport,
		config:              tracerConfig,
	}
	c.Transport = t
}

// NewDefaultClient returns a new default opensearch.Client enhanced with tracing.
func NewDefaultClient(opts ...Option) (*opensearch.Client, error) {
	return NewClient(opensearch.Config{}, opts...)
}

// NewClient returns a new opensearch.Client enhanced with tracing.
func NewClient(cfg opensearch.Config, opts ...Option) (*opensearch.Client, error) {
	if cfg.Transport == nil {
		cfg.Transport = WrapRoundTripper(http.DefaultTransport)
	} else {
		cfg.Transport = WrapRoundTripper(cfg.Transport)
	}
	c, err := opensearch.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	tracerConfig := defaultConfig()
	for _, fn := range opts {
		fn(tracerConfig)
	}
	t := &transport{
		opensearchtransport: c.Transport,
		config:              tracerConfig,
	}
	c.Transport = t
	return c, nil
}

// WrapRoundTripper wraps an http.RoundTripper with tracing.
func WrapRoundTripper(rt http.RoundTripper) http.RoundTripper {
	return &roundTripper{roundtripper: rt}
}

type roundTripper struct {
	roundtripper http.RoundTripper
}

// RoundTrip sets `ext.TargetHost` and `ext.TargetPort` tags on the span.
func (r *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Hostname and port are not decided when Peform() is called.
	if span, ok := tracer.SpanFromContext(req.Context()); ok {
		span.SetTag(ext.TargetHost, req.URL.Hostname())
		span.SetTag(ext.TargetPort, req.URL.Port())
	}
	return r.roundtripper.RoundTrip(req)
}

type transport struct {
	opensearchtransport opensearchtransport.Interface
	config              *config
}

// Perform traces the opensearch request.
func (t *transport) Perform(req *http.Request) (*http.Response, error) {
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(t.config.serviceName),
		tracer.SpanType(ext.SpanTypeOpenSearch),
		tracer.ResourceName(t.config.resourceNamer(req.URL.Path, req.Method)),
		tracer.Tag(ext.OpenSearchMethod, req.Method),
		tracer.Tag(ext.OpenSearchURL, req.URL.Path),
		tracer.Tag(ext.OpenSearchParams, req.URL.Query().Encode()),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.DBSystem, ext.DBSystemOpensearch),
		tracer.Tag(ext.NetworkDestinationName, req.URL.Hostname()),
	}
	span, ctx := tracer.StartSpanFromContext(req.Context(), namingschema.OpName(namingschema.OpenSearchOutbound), opts...)
	req = req.WithContext(ctx)
	contentEncoding := req.Header.Get("Content-Encoding")
	snip, rc, err := peek(req.Body, contentEncoding, int(req.ContentLength), bodyCutoff)
	if err == nil {
		span.SetTag(ext.OpenSearchBody, snip)
	} else {
		log.Debug("opensearch: failed to peek request body: %v", err)
	}
	req.Body = rc
	resp, err := t.opensearchtransport.Perform(req)
	if err != nil {
		span.Finish(tracer.WithError(err))
		return resp, err
	}
	span.SetTag(ext.HTTPCode, strconv.Itoa(resp.StatusCode))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snip, rc, err := peek(resp.Body, contentEncoding, int(resp.ContentLength), bodyCutoff)
		if err != nil {
			log.Debug("opensearch: failed to peek response body: %v", err)
			snip = http.StatusText(resp.StatusCode)
		}
		resp.Body = rc
		span.Finish(tracer.WithError(errors.New(snip)))
		return resp, nil
	}
	span.Finish()
	return resp, nil
}

// DiscoverNodes implements the opensearchtransport.Discoverable interface.
func (t *transport) DiscoverNodes() error {
	if dt, ok := t.opensearchtransport.(opensearchtransport.Discoverable); ok {
		return dt.DiscoverNodes()
	}
	return opensearch.ErrTransportMissingMethodDiscoverNodes
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
