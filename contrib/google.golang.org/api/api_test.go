// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package api

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/books/v1"
	"google.golang.org/api/civicinfo/v2"
	"google.golang.org/api/option"
	"google.golang.org/api/urlshortener/v1"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

var badRequestTransport roundTripperFunc = func(req *http.Request) (*http.Response, error) {
	res := &http.Response{
		Header:     make(http.Header),
		Request:    req,
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	return res, nil
}

func TestBooks(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc, err := books.NewService(context.Background(), option.WithHTTPClient(&http.Client{
		Transport: WrapRoundTripper(badRequestTransport),
	}))
	assert.NoError(t, err)
	svc.Bookshelves.
		List("montana.banana").
		Do()

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)

	s0 := spans[0]
	assert.Equal(t, "http.request", s0.OperationName())
	assert.Equal(t, "http", s0.Tag(ext.SpanType))
	assert.Equal(t, "google.books", s0.Tag(ext.ServiceName))
	assert.Equal(t, "books.bookshelves.list", s0.Tag(ext.ResourceName))
	assert.Equal(t, "400", s0.Tag(ext.HTTPCode))
	assert.Equal(t, "GET", s0.Tag(ext.HTTPMethod))
	assert.Equal(t, svc.BasePath+"books/v1/users/montana.banana/bookshelves?alt=json&prettyPrint=false", s0.Tag(ext.HTTPURL))
	assert.Equal(t, "google.golang.org/api", s0.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindClient, s0.Tag(ext.SpanKind))
}

func TestCivicInfo(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc, err := civicinfo.NewService(context.Background(), option.WithHTTPClient(&http.Client{
		Transport: WrapRoundTripper(badRequestTransport),
	}))
	assert.NoError(t, err)
	svc.Representatives.RepresentativeInfoByAddress().Do()

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)

	s0 := spans[0]
	assert.Equal(t, "http.request", s0.OperationName())
	assert.Equal(t, "http", s0.Tag(ext.SpanType))
	assert.Equal(t, "google.civicinfo", s0.Tag(ext.ServiceName))
	assert.Equal(t, "civicinfo.representatives.representativeInfoByAddress", s0.Tag(ext.ResourceName))
	assert.Equal(t, "400", s0.Tag(ext.HTTPCode))
	assert.Equal(t, "GET", s0.Tag(ext.HTTPMethod))
	assert.Equal(t, svc.BasePath+"civicinfo/v2/representatives?alt=json&prettyPrint=false", s0.Tag(ext.HTTPURL))
	assert.Equal(t, "google.golang.org/api", s0.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindClient, s0.Tag(ext.SpanKind))
}

func TestURLShortener(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc, err := urlshortener.NewService(context.Background(), option.WithHTTPClient(&http.Client{
		Transport: WrapRoundTripper(badRequestTransport),
	}))
	assert.NoError(t, err)
	svc.Url.
		List().
		Do()

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)

	s0 := spans[0]
	assert.Equal(t, "http.request", s0.OperationName())
	assert.Equal(t, "http", s0.Tag(ext.SpanType))
	assert.Equal(t, "google.urlshortener", s0.Tag(ext.ServiceName))
	assert.Equal(t, "urlshortener.url.list", s0.Tag(ext.ResourceName))
	assert.Equal(t, "400", s0.Tag(ext.HTTPCode))
	assert.Equal(t, "GET", s0.Tag(ext.HTTPMethod))
	assert.Equal(t, "https://www.googleapis.com/urlshortener/v1/url/history?alt=json&prettyPrint=false", s0.Tag(ext.HTTPURL))
	assert.Equal(t, "google.golang.org/api", s0.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindClient, s0.Tag(ext.SpanKind))
}

func TestWithEndpointMetadataDisabled(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc, err := civicinfo.NewService(context.Background(), option.WithHTTPClient(&http.Client{
		Transport: WrapRoundTripper(badRequestTransport, WithEndpointMetadataDisabled()),
	}))
	require.NoError(t, err)
	svc.Representatives.RepresentativeInfoByAddress().Do()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	s0 := spans[0]
	assert.Equal(t, "http.request", s0.OperationName())
	assert.Equal(t, "http", s0.Tag(ext.SpanType))
	assert.Equal(t, "google", s0.Tag(ext.ServiceName))
	assert.Equal(t, "GET civicinfo.googleapis.com", s0.Tag(ext.ResourceName))
	assert.Equal(t, "400", s0.Tag(ext.HTTPCode))
	assert.Equal(t, "GET", s0.Tag(ext.HTTPMethod))
	assert.Equal(t, svc.BasePath+"civicinfo/v2/representatives?alt=json&prettyPrint=false", s0.Tag(ext.HTTPURL))
	assert.Equal(t, "google.golang.org/api", s0.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindClient, s0.Tag(ext.SpanKind))
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		svc, err := books.New(&http.Client{
			Transport: WrapRoundTripper(badRequestTransport, opts...),
		})
		assert.NoError(t, err)
		svc.Bookshelves.List("montana.banana").Do()
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}

func BenchmarkWrapRoundTripper(b *testing.B) {
	b.ReportAllocs()

	svc, err := books.NewService(context.Background(), option.WithHTTPClient(&http.Client{
		Transport: WrapRoundTripper(badRequestTransport),
	}))
	require.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.Bookshelves.List("montana.banana").Do()
	}
}

func BenchmarkInitApiEndpointsTree(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		initAPIEndpointsTree()
	}
}

func TestTreeRegex(t *testing.T) {
	apiEndpoints, err := loadEndpointsFromJSON()
	require.NoError(t, err)

	for _, e := range apiEndpoints {
		_, err := regexp.Compile(e.PathRegex)
		assert.NoErrorf(t, err, "pathRegexp: %s", e.PathRegex)
	}
}
