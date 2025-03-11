// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package elastic

import (
	"context"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	elasticsearch8 "github.com/elastic/go-elasticsearch/v8"
	esapi8 "github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/stretchr/testify/assert"
)

func checkGETTraceV8(assert *assert.Assertions, mt mocktracer.Tracer) {
	span := mt.FinishedSpans()[0]
	assert.Equal("my-es-service", span.Tag(ext.ServiceName))
	assert.Equal("GET /twitter/_doc/?", span.Tag(ext.ResourceName))
	assert.Equal("/twitter/_doc/1", span.Tag("elasticsearch.url"))
	assert.Equal("GET", span.Tag("elasticsearch.method"))
	assert.Equal("127.0.0.1", span.Tag(ext.NetworkDestinationName))
	assert.Equal(componentName, span.Integration())
}

func checkErrTraceV8(assert *assert.Assertions, mt mocktracer.Tracer) {
	span := mt.FinishedSpans()[0]
	assert.Equal("my-es-service", span.Tag(ext.ServiceName))
	assert.Equal("GET /not-real-index/_doc/?", span.Tag(ext.ResourceName))
	assert.Equal("/not-real-index/_doc/1", span.Tag("elasticsearch.url"))
	assert.NotEmpty(span.Tag(ext.ErrorMsg))
	assert.Equal("127.0.0.1", span.Tag(ext.NetworkDestinationName))
	assert.Equal(componentName, span.Integration())
}

func TestClientV8(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	cfg := elasticsearch8.Config{
		Transport: NewRoundTripper(WithService("my-es-service")),
		Addresses: []string{
			elasticV8URL,
		},
	}
	client, err := elasticsearch8.NewClient(cfg)
	assert.NoError(err)

	_, err = esapi8.IndexRequest{
		Index:      "twitter",
		DocumentID: "1",
		Body:       strings.NewReader(`{"user": "test", "message": "hello"}`),
	}.Do(context.Background(), client)
	assert.NoError(err)

	mt.Reset()
	_, err = esapi8.GetRequest{
		Index:      "twitter",
		DocumentID: "1",
	}.Do(context.Background(), client)
	assert.NoError(err)
	checkGETTraceV8(assert, mt)

	mt.Reset()
	_, err = esapi8.GetRequest{
		Index:      "not-real-index",
		DocumentID: "1",
	}.Do(context.Background(), client)
	assert.NoError(err)
	checkErrTraceV8(assert, mt)

}

func TestClientErrorCutoffV8(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	oldCutoff := bodyCutoff
	defer func() {
		bodyCutoff = oldCutoff
	}()
	bodyCutoff = 10

	cfg := elasticsearch8.Config{
		Transport: NewRoundTripper(WithService("my-es-service")),
		Addresses: []string{
			elasticV8URL,
		},
	}
	client, err := elasticsearch8.NewClient(cfg)
	assert.NoError(err)

	_, err = esapi8.GetRequest{
		Index:      "not-real-index",
		DocumentID: "1",
	}.Do(context.Background(), client)
	assert.NoError(err)

	span := mt.FinishedSpans()[0]
	assert.NotEmpty(span.Tag(ext.ErrorMsg))
}

func TestClientV8Failure(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	cfg := elasticsearch8.Config{
		Transport: NewRoundTripper(WithService("my-es-service")),
		Addresses: []string{
			"http://127.0.0.1:9207", // inexistent service, it must fail
		},
	}
	client, err := elasticsearch8.NewClient(cfg)
	assert.NoError(err)

	_, err = esapi8.IndexRequest{
		Index:      "twitter",
		DocumentID: "1",
		Body:       strings.NewReader(`{"user": "test", "message": "hello"}`),
	}.Do(context.Background(), client)
	assert.Error(err)

	spans := mt.FinishedSpans()
	assert.NotEmpty(spans[0].Tag(ext.ErrorMsg))
}

func TestResourceNamerSettingsV8(t *testing.T) {
	staticName := "static resource name"
	staticNamer := func(_, _ string) string {
		return staticName
	}

	t.Run("default", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		cfg := elasticsearch8.Config{
			Transport: NewRoundTripper(),
			Addresses: []string{
				elasticV8URL,
			},
		}
		client, err := elasticsearch8.NewClient(cfg)
		assert.NoError(t, err)

		_, err = esapi8.GetRequest{
			Index:      "logs_2017_05/event/_search",
			DocumentID: "1",
		}.Do(context.Background(), client)

		span := mt.FinishedSpans()[0]
		assert.Equal(t, "GET /logs_?_?/event/_search/_doc/?", span.Tag(ext.ResourceName))
	})

	t.Run("custom", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		cfg := elasticsearch8.Config{
			Transport: NewRoundTripper(WithResourceNamer(staticNamer)),
			Addresses: []string{
				elasticV8URL,
			},
		}
		client, err := elasticsearch8.NewClient(cfg)
		assert.NoError(t, err)

		_, err = esapi8.GetRequest{
			Index:      "logs_2017_05/event/_search",
			DocumentID: "1",
		}.Do(context.Background(), client)

		span := mt.FinishedSpans()[0]
		assert.Equal(t, staticName, span.Tag(ext.ResourceName))
	})
}

func TestAnalyticsSettingsV8(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...ClientOption) {

		cfg := elasticsearch8.Config{
			Transport: NewRoundTripper(opts...),
			Addresses: []string{
				elasticV8URL,
			},
		}
		client, err := elasticsearch8.NewClient(cfg)
		assert.NoError(t, err)

		_, err = esapi8.IndexRequest{
			Index:      "twitter",
			DocumentID: "1",
			Body:       strings.NewReader(`{"user": "test", "message": "hello"}`),
		}.Do(context.Background(), client)
		assert.NoError(t, err)

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
		testutils.SetGlobalAnalyticsRate(t, 0.4)

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
		testutils.SetGlobalAnalyticsRate(t, 0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}
