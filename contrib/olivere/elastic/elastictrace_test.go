// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package elastic

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/stretchr/testify/assert"
	elasticv3 "gopkg.in/olivere/elastic.v3"
	elasticv5 "gopkg.in/olivere/elastic.v5"
)

const (
	elasticV5URL   = "http://127.0.0.1:9201"
	elasticV3URL   = "http://127.0.0.1:9200"
	elasticFakeURL = "http://127.0.0.1:29201"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestClientV5(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	tc := NewHTTPClient(WithServiceName("my-es-service"))
	client, err := elasticv5.NewClient(
		elasticv5.SetURL(elasticV5URL),
		elasticv5.SetHttpClient(tc),
		elasticv5.SetSniff(false),
		elasticv5.SetHealthcheck(false),
	)
	assert.NoError(err)

	_, err = client.Index().
		Index("twitter").Id("1").
		Type("tweet").
		BodyString(`{"user": "test", "message": "hello"}`).
		Do(context.TODO())
	assert.NoError(err)
	checkPUTTrace(assert, mt, "127.0.0.1")

	mt.Reset()
	_, err = client.Get().Index("twitter").Type("tweet").
		Id("1").Do(context.TODO())
	assert.NoError(err)
	checkGETTrace(assert, mt, "127.0.0.1")

	mt.Reset()
	_, err = client.Get().Index("not-real-index").
		Id("1").Do(context.TODO())
	assert.Error(err)
	checkErrTrace(assert, mt, "127.0.0.1")
}

func TestClientV5Gzip(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	tc := NewHTTPClient(WithServiceName("my-es-service"))
	client, err := elasticv5.NewClient(
		elasticv5.SetURL(elasticV5URL),
		elasticv5.SetHttpClient(tc),
		elasticv5.SetSniff(false),
		elasticv5.SetHealthcheck(false),
		elasticv5.SetGzip(true),
	)
	assert.NoError(err)

	_, err = client.Index().
		Index("twitter").Id("1").
		Type("tweet").
		BodyString(`{"user": "test", "message": "hello"}`).
		Do(context.TODO())
	assert.NoError(err)
	checkPUTTrace(assert, mt, "127.0.0.1")

	mt.Reset()
	_, err = client.Get().Index("twitter").Type("tweet").
		Id("1").Do(context.TODO())
	assert.NoError(err)
	checkGETTrace(assert, mt, "127.0.0.1")

	mt.Reset()
	_, err = client.Get().Index("not-real-index").
		Id("1").Do(context.TODO())
	assert.Error(err)
	checkErrTrace(assert, mt, "127.0.0.1")
}

func TestClientErrorCutoffV3(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	oldCutoff := bodyCutoff
	defer func() {
		bodyCutoff = oldCutoff
	}()
	bodyCutoff = 10

	tc := NewHTTPClient(WithServiceName("my-es-service"))
	client, err := elasticv5.NewClient(
		elasticv5.SetURL(elasticV3URL),
		elasticv5.SetHttpClient(tc),
		elasticv5.SetSniff(false),
		elasticv5.SetHealthcheck(false),
	)
	assert.NoError(err)

	_, err = client.Index().
		Index("twitter").Id("1").
		Type("tweet").
		BodyString(`{"user": "test", "message": "hello"}`).
		Do(context.TODO())
	assert.NoError(err)

	span := mt.FinishedSpans()[0]
	assert.True(strings.HasPrefix(span.Tag("elasticsearch.body").(string), `{"user": "`))
}

func TestClientErrorCutoffV5(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	oldCutoff := bodyCutoff
	defer func() {
		bodyCutoff = oldCutoff
	}()
	bodyCutoff = 10

	tc := NewHTTPClient(WithServiceName("my-es-service"))
	client, err := elasticv5.NewClient(
		elasticv5.SetURL(elasticV5URL),
		elasticv5.SetHttpClient(tc),
		elasticv5.SetSniff(false),
		elasticv5.SetHealthcheck(false),
	)
	assert.NoError(err)

	_, err = client.Get().Index("not-real-index").
		Id("1").Do(context.TODO())
	assert.Error(err)

	span := mt.FinishedSpans()[0]
	assert.True(strings.HasPrefix(span.Tag(ext.ErrorMsg).(string), `{"error":{`))
}

func TestClientV3(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	tc := NewHTTPClient(WithServiceName("my-es-service"))
	client, err := elasticv3.NewClient(
		elasticv3.SetURL(elasticV3URL),
		elasticv3.SetHttpClient(tc),
		elasticv3.SetSniff(false),
		elasticv3.SetHealthcheck(false),
	)
	assert.NoError(err)

	_, err = client.Index().
		Index("twitter").Id("1").
		Type("tweet").
		BodyString(`{"user": "test", "message": "hello"}`).
		DoC(context.TODO())
	assert.NoError(err)
	checkPUTTrace(assert, mt, "127.0.0.1")

	mt.Reset()
	_, err = client.Get().Index("twitter").Type("tweet").
		Id("1").DoC(context.TODO())
	assert.NoError(err)
	checkGETTrace(assert, mt, "127.0.0.1")

	mt.Reset()
	_, err = client.Get().Index("not-real-index").
		Id("1").DoC(context.TODO())
	assert.Error(err)
	checkErrTrace(assert, mt, "127.0.0.1")
}

func TestClientV3Failure(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	tc := NewHTTPClient(WithServiceName("my-es-service"))
	client, err := elasticv3.NewClient(
		// inexistent service, it must fail
		elasticv3.SetURL(elasticFakeURL),
		elasticv3.SetHttpClient(tc),
		elasticv3.SetSniff(false),
		elasticv3.SetHealthcheck(false),
	)
	assert.NoError(err)

	_, err = client.Index().
		Index("twitter").Id("1").
		Type("tweet").
		BodyString(`{"user": "test", "message": "hello"}`).
		DoC(context.TODO())
	assert.Error(err)

	spans := mt.FinishedSpans()
	checkPUTTrace(assert, mt, "127.0.0.1")

	assert.NotEmpty(spans[0].Tag(ext.Error))
	assert.Equal("*net.OpError", spans[0].Tag(ext.ErrorType))
}

func TestClientV5Failure(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	tc := NewHTTPClient(WithServiceName("my-es-service"))
	client, err := elasticv5.NewClient(
		// inexistent service, it must fail
		elasticv5.SetURL(elasticFakeURL),
		elasticv5.SetHttpClient(tc),
		elasticv5.SetSniff(false),
		elasticv5.SetHealthcheck(false),
	)
	assert.NoError(err)

	_, err = client.Index().
		Index("twitter").Id("1").
		Type("tweet").
		BodyString(`{"user": "test", "message": "hello"}`).
		Do(context.TODO())
	assert.Error(err)

	spans := mt.FinishedSpans()
	checkPUTTrace(assert, mt, "127.0.0.1")

	assert.NotEmpty(spans[0].Tag(ext.Error))
	assert.Equal("*net.OpError", spans[0].Tag(ext.ErrorType))
}

func checkPUTTrace(assert *assert.Assertions, mt mocktracer.Tracer, host string) {
	span := mt.FinishedSpans()[0]
	assert.Equal("my-es-service", span.Tag(ext.ServiceName))
	assert.Equal("PUT /twitter/tweet/?", span.Tag(ext.ResourceName))
	assert.Equal("/twitter/tweet/1", span.Tag("elasticsearch.url"))
	assert.Equal("PUT", span.Tag("elasticsearch.method"))
	assert.Equal(`{"user": "test", "message": "hello"}`, span.Tag("elasticsearch.body"))
	assert.Equal("olivere/elastic", span.Tag(ext.Component))
	assert.Equal("olivere/elastic", span.Integration())
	assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal("elasticsearch", span.Tag(ext.DBSystem))
	assert.Equal(host, span.Tag(ext.NetworkDestinationName))
}

func checkGETTrace(assert *assert.Assertions, mt mocktracer.Tracer, host string) {
	span := mt.FinishedSpans()[0]
	assert.Equal("my-es-service", span.Tag(ext.ServiceName))
	assert.Equal("GET /twitter/tweet/?", span.Tag(ext.ResourceName))
	assert.Equal("/twitter/tweet/1", span.Tag("elasticsearch.url"))
	assert.Equal("GET", span.Tag("elasticsearch.method"))
	assert.Equal("olivere/elastic", span.Tag(ext.Component))
	assert.Equal("olivere/elastic", span.Integration())
	assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal("elasticsearch", span.Tag(ext.DBSystem))
	assert.Equal(host, span.Tag(ext.NetworkDestinationName))
}

func checkErrTrace(assert *assert.Assertions, mt mocktracer.Tracer, host string) {
	span := mt.FinishedSpans()[0]
	assert.Equal("my-es-service", span.Tag(ext.ServiceName))
	assert.Equal("GET /not-real-index/_all/?", span.Tag(ext.ResourceName))
	assert.Equal("/not-real-index/_all/1", span.Tag("elasticsearch.url"))
	assert.NotEmpty(span.Tag(ext.Error))
	assert.Equal("*errors.errorString", fmt.Sprintf("%T", span.Tag(ext.Error).(error)))
	assert.Equal("olivere/elastic", span.Tag(ext.Component))
	assert.Equal("olivere/elastic", span.Integration())
	assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal("elasticsearch", span.Tag(ext.DBSystem))
	assert.Equal(host, span.Tag(ext.NetworkDestinationName))
}

func TestResourceNamerSettings(t *testing.T) {
	staticName := "static resource name"
	staticNamer := func(url, method string) string {
		return staticName
	}

	t.Run("default", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		tc := NewHTTPClient()
		client, err := elasticv5.NewClient(
			elasticv5.SetURL(elasticV3URL),
			elasticv5.SetHttpClient(tc),
			elasticv5.SetSniff(false),
			elasticv5.SetHealthcheck(false),
		)
		assert.NoError(t, err)

		_, err = client.Get().
			Index("logs_2016_05/event/_search").
			Type("tweet").
			Id("1").Do(context.TODO())

		span := mt.FinishedSpans()[0]
		assert.Equal(t, "GET /logs_?_?/event/_search/tweet/?", span.Tag(ext.ResourceName))
	})

	t.Run("custom", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		tc := NewHTTPClient(WithResourceNamer(staticNamer))
		client, err := elasticv5.NewClient(
			elasticv5.SetURL(elasticV3URL),
			elasticv5.SetHttpClient(tc),
			elasticv5.SetSniff(false),
			elasticv5.SetHealthcheck(false),
		)
		assert.NoError(t, err)

		_, err = client.Get().
			Index("logs_2016_05/event/_search").
			Type("tweet").
			Id("1").Do(context.TODO())

		span := mt.FinishedSpans()[0]
		assert.Equal(t, staticName, span.Tag(ext.ResourceName))
	})
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...ClientOption) {
		tc := NewHTTPClient(opts...)
		client, err := elasticv5.NewClient(
			elasticv5.SetURL(elasticV5URL),
			elasticv5.SetHttpClient(tc),
			elasticv5.SetSniff(false),
			elasticv5.SetHealthcheck(false),
		)
		assert.NoError(t, err)

		_, err = client.Index().
			Index("twitter").Id("1").
			Type("tweet").
			BodyString(`{"user": "test", "message": "hello"}`).
			Do(context.TODO())
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
