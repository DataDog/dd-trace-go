package elastic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	elasticv3 "gopkg.in/olivere/elastic.v3"
	elasticv5 "gopkg.in/olivere/elastic.v5"

	"testing"
)

const debug = false

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
		elasticv5.SetURL("http://127.0.0.1:9201"),
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
	checkPUTTrace(assert, mt)

	mt.Reset()
	_, err = client.Get().Index("twitter").Type("tweet").
		Id("1").Do(context.TODO())
	assert.NoError(err)
	checkGETTrace(assert, mt)

	mt.Reset()
	_, err = client.Get().Index("not-real-index").
		Id("1").Do(context.TODO())
	assert.Error(err)
	checkErrTrace(assert, mt)
}

func TestClientV5Gzip(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	tc := NewHTTPClient(WithServiceName("my-es-service"))
	client, err := elasticv5.NewClient(
		elasticv5.SetURL("http://127.0.0.1:9201"),
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
	checkPUTTrace(assert, mt)

	mt.Reset()
	_, err = client.Get().Index("twitter").Type("tweet").
		Id("1").Do(context.TODO())
	assert.NoError(err)
	checkGETTrace(assert, mt)

	mt.Reset()
	_, err = client.Get().Index("not-real-index").
		Id("1").Do(context.TODO())
	assert.Error(err)
	checkErrTrace(assert, mt)
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
		elasticv5.SetURL("http://127.0.0.1:9200"),
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
	assert.Equal(`{"user": "`, span.Tag("elasticsearch.body"))
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
		elasticv5.SetURL("http://127.0.0.1:9201"),
		elasticv5.SetHttpClient(tc),
		elasticv5.SetSniff(false),
		elasticv5.SetHealthcheck(false),
	)
	assert.NoError(err)

	_, err = client.Get().Index("not-real-index").
		Id("1").Do(context.TODO())
	assert.Error(err)

	span := mt.FinishedSpans()[0]
	assert.Equal(`{"error":{`, span.Tag(ext.Error).(error).Error())
}

func TestClientV3(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	tc := NewHTTPClient(WithServiceName("my-es-service"))
	client, err := elasticv3.NewClient(
		elasticv3.SetURL("http://127.0.0.1:9200"),
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
	checkPUTTrace(assert, mt)

	mt.Reset()
	_, err = client.Get().Index("twitter").Type("tweet").
		Id("1").DoC(context.TODO())
	assert.NoError(err)
	checkGETTrace(assert, mt)

	mt.Reset()
	_, err = client.Get().Index("not-real-index").
		Id("1").DoC(context.TODO())
	assert.Error(err)
	checkErrTrace(assert, mt)
}

func TestClientV3Failure(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	tc := NewHTTPClient(WithServiceName("my-es-service"))
	client, err := elasticv3.NewClient(
		// inexistent service, it must fail
		elasticv3.SetURL("http://127.0.0.1:29200"),
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
	checkPUTTrace(assert, mt)

	assert.NotEmpty(spans[0].Tag(ext.Error))
	assert.Equal("*net.OpError", fmt.Sprintf("%T", spans[0].Tag(ext.Error).(error)))
}

func TestClientV5Failure(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	tc := NewHTTPClient(WithServiceName("my-es-service"))
	client, err := elasticv5.NewClient(
		// inexistent service, it must fail
		elasticv5.SetURL("http://127.0.0.1:29201"),
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
	checkPUTTrace(assert, mt)

	assert.NotEmpty(spans[0].Tag(ext.Error))
	assert.Equal("*net.OpError", fmt.Sprintf("%T", spans[0].Tag(ext.Error).(error)))
}

func checkPUTTrace(assert *assert.Assertions, mt mocktracer.Tracer) {
	span := mt.FinishedSpans()[0]
	assert.Equal("my-es-service", span.Tag(ext.ServiceName))
	assert.Equal("PUT /twitter/tweet/?", span.Tag(ext.ResourceName))
	assert.Equal("/twitter/tweet/1", span.Tag("elasticsearch.url"))
	assert.Equal("PUT", span.Tag("elasticsearch.method"))
	assert.Equal(`{"user": "test", "message": "hello"}`, span.Tag("elasticsearch.body"))
}

func checkGETTrace(assert *assert.Assertions, mt mocktracer.Tracer) {
	span := mt.FinishedSpans()[0]
	assert.Equal("my-es-service", span.Tag(ext.ServiceName))
	assert.Equal("GET /twitter/tweet/?", span.Tag(ext.ResourceName))
	assert.Equal("/twitter/tweet/1", span.Tag("elasticsearch.url"))
	assert.Equal("GET", span.Tag("elasticsearch.method"))
}

func checkErrTrace(assert *assert.Assertions, mt mocktracer.Tracer) {
	span := mt.FinishedSpans()[0]
	assert.Equal("my-es-service", span.Tag(ext.ServiceName))
	assert.Equal("GET /not-real-index/_all/?", span.Tag(ext.ResourceName))
	assert.Equal("/not-real-index/_all/1", span.Tag("elasticsearch.url"))
	assert.NotEmpty(span.Tag(ext.Error))
	assert.Equal("*errors.errorString", fmt.Sprintf("%T", span.Tag(ext.Error).(error)))
}

func TestQuantize(t *testing.T) {
	for _, tc := range []struct {
		url, method string
		expected    string
	}{
		{
			url:      "/twitter/tweets",
			method:   "POST",
			expected: "POST /twitter/tweets",
		},
		{
			url:      "/logs_2016_05/event/_search",
			method:   "GET",
			expected: "GET /logs_?_?/event/_search",
		},
		{
			url:      "/twitter/tweets/123",
			method:   "GET",
			expected: "GET /twitter/tweets/?",
		},
		{
			url:      "/logs_2016_05/event/123",
			method:   "PUT",
			expected: "PUT /logs_?_?/event/?",
		},
	} {
		assert.Equal(t, tc.expected, quantize(tc.url, tc.method))
	}
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
			elasticv5.SetURL("http://127.0.0.1:9200"),
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
			elasticv5.SetURL("http://127.0.0.1:9200"),
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

func TestPeek(t *testing.T) {
	assert := assert.New(t)

	for _, tt := range [...]struct {
		max  int    // content length
		txt  string // stream
		n    int    // bytes to peek at
		snip string // expected snippet
		err  error  // expected error
	}{
		0: {
			// extract 3 bytes from a content of length 7
			txt:  "ABCDEFG",
			max:  7,
			n:    3,
			snip: "ABC",
		},
		1: {
			// extract 7 bytes from a content of length 7
			txt:  "ABCDEFG",
			max:  7,
			n:    7,
			snip: "ABCDEFG",
		},
		2: {
			// extract 100 bytes from a content of length 9 (impossible scenario)
			txt:  "ABCDEFG",
			max:  9,
			n:    100,
			snip: "ABCDEFG",
		},
		3: {
			// extract 5 bytes from a content of length 2 (impossible scenario)
			txt:  "ABCDEFG",
			max:  2,
			n:    5,
			snip: "AB",
		},
		4: {
			txt:  "ABCDEFG",
			max:  0,
			n:    1,
			snip: "A",
		},
		5: {
			n:   4,
			max: 4,
			err: errors.New("empty stream"),
		},
		6: {
			txt:  "ABCDEFG",
			n:    4,
			max:  -1,
			snip: "ABCD",
		},
	} {
		var readcloser io.ReadCloser
		if tt.txt != "" {
			readcloser = ioutil.NopCloser(bytes.NewBufferString(tt.txt))
		}
		snip, rc, err := peek(readcloser, "", tt.max, tt.n)
		assert.Equal(tt.err, err)
		assert.Equal(tt.snip, snip)

		if readcloser != nil {
			// if a non-nil io.ReadCloser was sent, the returned io.ReadCloser
			// must always return the entire original content.
			all, err := ioutil.ReadAll(rc)
			assert.Nil(err)
			assert.Equal(tt.txt, string(all))
		}
	}
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...ClientOption) {
		tc := NewHTTPClient(opts...)
		client, err := elasticv5.NewClient(
			elasticv5.SetURL("http://127.0.0.1:9201"),
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
