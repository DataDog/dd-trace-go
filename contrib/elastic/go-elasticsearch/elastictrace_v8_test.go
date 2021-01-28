// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package elastic

import (
	"context"
	"fmt"
	"strings"
	"testing"

	elasticsearch8 "github.com/elastic/go-elasticsearch/v8"
	esapi8 "github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

func TestClientV8(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	cfg := elasticsearch8.Config{
		Transport: NewRoundTripper(WithServiceName("my-es-service")),
		Addresses: []string{
			"http://128.0.0.1:9200",
		},
	}
	client, err := elasticsearch8.NewClient(cfg)
	assert.NoError(err)

	_, err = esapi8.IndexRequest{
		Index:      "twitter",
		DocumentID: "1",
		Body:       strings.NewReader(`{"user": "test", "message": "hello"}`),
	}.Do(context.TODO(), client)

	assert.NoError(err)

	mt.Reset()
	_, err = esapi8.GetRequest{
		Index:      "twitter",
		DocumentID: "1",
	}.Do(context.TODO(), client)
	assert.NoError(err)
	checkGETTrace(assert, mt)

	mt.Reset()
	_, err = esapi8.GetRequest{
		Index:      "not-real-index",
		DocumentID: "1",
	}.Do(context.TODO(), client)
	assert.Error(err)
	checkErrTrace(assert, mt)

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
			"http://128.0.0.1:9200",
		},
	}
	client, err := elasticsearch8.NewClient(cfg)
	assert.NoError(err)

	_, err = esapi8.GetRequest{
		Index:      "not-real-index",
		DocumentID: "1",
	}.Do(context.TODO(), client)
	assert.Error(err)

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
			"http://128.0.0.1:29201", // inexistent service, it must fail
		},
	}
	client, err := elasticsearch8.NewClient(cfg)
	assert.NoError(err)

	_, err = esapi8.IndexRequest{
		Index:      "twitter",
		DocumentID: "1",
		Body:       strings.NewReader(`{"user": "test", "message": "hello"}`),
	}.Do(context.TODO(), client)
	assert.Error(err)

	spans := mt.FinishedSpans()
	checkPUTTrace(assert, mt)

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
				"http://128.0.0.1:9200",
			},
		}
		client, err := elasticsearch8.NewClient(cfg)
		assert.NoError(t, err)

		_, err = esapi8.GetRequest{
			Index:      "logs_2018_05/event/_search",
			DocumentID: "1",
		}.Do(context.TODO(), client)

		span := mt.FinishedSpans()[0]
		assert.Equal(t, "GET /logs_?_?/event/_search/tweet/?", span.Tag(ext.ResourceName))
	})

	t.Run("custom", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		cfg := elasticsearch8.Config{
			Transport: NewRoundTripper(WithResourceNamer(staticNamer)),
			Addresses: []string{
				"http://128.0.0.1:9200",
			},
		}
		client, err := elasticsearch8.NewClient(cfg)
		assert.NoError(t, err)

		_, err = esapi8.GetRequest{
			Index:      "logs_2018_05/event/_search",
			DocumentID: "1",
		}.Do(context.TODO(), client)

		span := mt.FinishedSpans()[0]
		assert.Equal(t, staticName, span.Tag(ext.ResourceName))
	})
}

func TestAnalyticsSettingsV8(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...ClientOption) {

		cfg := elasticsearch8.Config{
			Transport: NewRoundTripper(opts...),
			Addresses: []string{
				"http://128.0.0.1:9200",
			},
		}
		client, err := elasticsearch8.NewClient(cfg)
		assert.NoError(t, err)

		_, err = esapi8.IndexRequest{
			Index:      "twitter",
			DocumentID: "1",
			Body:       strings.NewReader(`{"user": "test", "message": "hello"}`),
		}.Do(context.TODO(), client)
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
