// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package elastic provides functions to trace the github.com/elastic/go-elasticsearch packages.
package elastic // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/elastic/go-elasticsearch

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"regexp"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// NewRoundTripper returns a new http.Client which traces requests under the given service name.
func NewRoundTripper(opts ...ClientOption) http.RoundTripper {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return &roundTripper{config: *cfg}
}

// bodyCutoff specifies the maximum number of bytes that will be stored as a tag
// value obtained from an HTTP request or response body.
var bodyCutoff = 5 * 1024

// TracedRoundTripper is a traced HTTP transport that captures Elasticsearch spans.
type roundTripper struct {
	config clientConfig
}

// RoundTrip satisfies the RoundTripper interface, wraps the sub Transport and
// captures a span of the Elasticsearch request.
func (t *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	url := req.URL.Path
	method := req.Method
	resource := t.config.resourceNamer(url, method)
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(t.config.serviceName),
		tracer.SpanType(ext.SpanTypeElasticSearch),
		tracer.ResourceName(resource),
		tracer.Tag("elasticsearch.method", method),
		tracer.Tag("elasticsearch.url", url),
		tracer.Tag("elasticsearch.params", req.URL.Query().Encode()),
	}
	if !math.IsNaN(t.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, t.config.analyticsRate))
	}
	span, _ := tracer.StartSpanFromContext(req.Context(), "elasticsearch.query", opts...)
	defer span.Finish()

	contentEncoding := req.Header.Get("Content-Encoding")
	snip, rc, err := peek(req.Body, contentEncoding, int(req.ContentLength), bodyCutoff)
	if err == nil {
		span.SetTag("elasticsearch.body", snip)
	}
	req.Body = rc
	// process using the standard transport
	res, err := t.config.transport.RoundTrip(req)
	if err != nil {
		// roundtrip error
		span.SetTag(ext.Error, err)
	} else if res.StatusCode < 200 || res.StatusCode > 299 {
		// HTTP error
		snip, rc, err := peek(res.Body, contentEncoding, int(res.ContentLength), bodyCutoff)
		if err != nil {
			snip = http.StatusText(res.StatusCode)
		}
		span.SetTag(ext.Error, errors.New(snip))
		res.Body = rc
	}
	if res != nil {
		span.SetTag(ext.HTTPCode, strconv.Itoa(res.StatusCode))
	}
	return res, err

}

var (
	idRegexp         = regexp.MustCompile("/([0-9]+)([/\\?]|$)")
	idPlaceholder    = []byte("/?$2")
	indexRegexp      = regexp.MustCompile("[0-9]{2,}")
	indexPlaceholder = []byte("?")
)

// quantize quantizes an Elasticsearch to extract a meaningful resource from the request.
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
func peek(rc io.ReadCloser, encoding string, max, n int) (string, io.ReadCloser, error) {
	if rc == nil {
		return "", rc, errors.New("empty stream")
	}
	if max > 0 && max < n {
		n = max
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
		gzr, err := gzip.NewReader(bytes.NewReader(snip))
		if err != nil {
			// snip wasn't gzip; return it as is
			return string(snip), rc2, nil
		}
		defer gzr.Close()
		snip, err = ioutil.ReadAll(gzr)
	}
	return string(snip), rc2, err
}
