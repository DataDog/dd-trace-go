// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package elastic

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	elasticsearch8 "github.com/elastic/go-elasticsearch/v8"
	esapi8 "github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func checkGETTraceV8(assert *assert.Assertions, mt mocktracer.Tracer) {
	span := mt.FinishedSpans()[0]
	assert.Equal("my-es-service", span.Tag(ext.ServiceName))
	assert.Equal("GET /twitter/_doc/?", span.Tag(ext.ResourceName))
	assert.Equal("/twitter/_doc/1", span.Tag("elasticsearch.url"))
	assert.Equal("GET", span.Tag("elasticsearch.method"))
	assert.Equal("127.0.0.1", span.Tag(ext.NetworkDestinationName))
}

func checkErrTraceV8(assert *assert.Assertions, mt mocktracer.Tracer) {
	span := mt.FinishedSpans()[0]
	assert.Equal("my-es-service", span.Tag(ext.ServiceName))
	assert.Equal("GET /not-real-index/_doc/?", span.Tag(ext.ResourceName))
	assert.Equal("/not-real-index/_doc/1", span.Tag("elasticsearch.url"))
	assert.NotEmpty(span.Tag(ext.Error))
	assert.Equal("*errors.errorString", fmt.Sprintf("%T", span.Tag(ext.Error).(error)))
	assert.Equal("127.0.0.1", span.Tag(ext.NetworkDestinationName))
}

func TestClientV8(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	cfg := elasticsearch8.Config{
		Transport: NewRoundTripper(WithServiceName("my-es-service")),
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
		Transport: NewRoundTripper(WithServiceName("my-es-service")),
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
	assert.Equal(`{"error":{`, span.Tag(ext.Error).(error).Error())
}

func TestClientV8Failure(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	cfg := elasticsearch8.Config{
		Transport: NewRoundTripper(WithServiceName("my-es-service")),
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
	assert.NotEmpty(spans[0].Tag(ext.Error))
	assert.Equal("*net.OpError", fmt.Sprintf("%T", spans[0].Tag(ext.Error).(error)))
}

func TestResourceNamerSettingsV8(t *testing.T) {
	staticName := "static resource name"
	staticNamer := func(url, method string) string {
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

func TestNamingSchema(t *testing.T) {
	genSpans := func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []ClientOption
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()
		cfg := elasticsearch8.Config{
			Transport: NewRoundTripper(opts...),
			Addresses: []string{
				elasticV8URL,
			},
		}
		client, err := elasticsearch8.NewClient(cfg)
		require.NoError(t, err)

		_, err = esapi8.IndexRequest{
			Index:      "twitter",
			DocumentID: "1",
			Body:       strings.NewReader(`{"user": "test", "message": "hello"}`),
		}.Do(context.Background(), client)
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		return spans
	}
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "elasticsearch.query", spans[0].OperationName())
	}
	assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "elasticsearch.query", spans[0].OperationName())
	}
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             []string{"elastic.client"},
		WithDDService:            []string{"elastic.client"},
		WithDDServiceAndOverride: []string{namingschematest.TestServiceOverride},
	}
	t.Run("ServiceName", namingschematest.NewServiceNameTest(genSpans, wantServiceNameV0))
	t.Run("SpanName", namingschematest.NewSpanNameTest(genSpans, assertOpV0, assertOpV1))
}
