// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/normalizer"

	"github.com/stretchr/testify/assert"
)

func TestWithHeaderTags(t *testing.T) {
	setupReq := func(opts ...Option) *http.Request {
		r := httptest.NewRequest("GET", "/test", nil)
		r.Header.Set("h!e@a-d.e*r", "val")
		r.Header.Add("h!e@a-d.e*r", "val2")
		r.Header.Set("2header", "2val")
		r.Header.Set("3header", "3val")
		w := httptest.NewRecorder()
		router(opts...).ServeHTTP(w, r)
		return r
	}
	t.Run("default-off", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		htArgs := []string{"h!e@a-d.e*r", "2header", "3header"}
		setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]
		for _, arg := range htArgs {
			_, tag := normalizer.HeaderTag(arg)
			assert.NotContains(s.Tags(), tag)
		}
	})
	t.Run("integration", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		htArgs := []string{"h!e@a-d.e*r", "2header:tag"}
		r := setupReq(WithHeaderTags(htArgs))
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		for _, arg := range htArgs {
			header, tag := normalizer.HeaderTag(arg)
			assert.Equal(strings.Join(r.Header.Values(header), ","), s.Tags()[tag])
		}
	})

	t.Run("global", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		header, tag := normalizer.HeaderTag("3header")
		globalconfig.SetHeaderTag(header, tag)

		r := setupReq()
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		assert.Equal(strings.Join(r.Header.Values(header), ","), s.Tags()[tag])
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		globalH, globalT := normalizer.HeaderTag("3header")
		globalconfig.SetHeaderTag(globalH, globalT)

		htArgs := []string{"h!e@a-d.e*r", "2header:tag"}
		r := setupReq(WithHeaderTags(htArgs))
		spans := mt.FinishedSpans()
		assert := assert.New(t)
		assert.Equal(len(spans), 1)
		s := spans[0]

		for _, arg := range htArgs {
			header, tag := normalizer.HeaderTag(arg)
			assert.Equal(strings.Join(r.Header.Values(header), ","), s.Tags()[tag])
		}
		assert.NotContains(s.Tags(), globalT)
	})

	t.Run("wrap-handler", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		htArgs := []string{"h!e@a-d.e*r", "2header", "3header"}

		handler := WrapHandler(http.HandlerFunc(handler200), "my-service", "my-resource",
			WithHeaderTags(htArgs),
		)

		url := "/"
		r := httptest.NewRequest("GET", url, nil)
		r.Header.Set("h!e@a-d.e*r", "val")
		r.Header.Add("h!e@a-d.e*r", "val2")
		r.Header.Set("2header", "2val")
		r.Header.Set("3header", "3val")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assert := assert.New(t)
		assert.Equal(200, w.Code)
		assert.Equal("OK\n", w.Body.String())

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))

		s := spans[0]
		assert.Equal("http.request", s.OperationName())

		for _, arg := range htArgs {
			header, tag := normalizer.HeaderTag(arg)
			assert.Equal(strings.Join(r.Header.Values(header), ","), s.Tags()[tag])
		}
	})
}

func TestHttpTracer200(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	url := "/200"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router().ServeHTTP(w, r)

	assert := assert.New(t)
	assert.Equal(200, w.Code)
	assert.Equal("OK\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("GET "+url, s.Tag(ext.ResourceName))
	assert.Equal("200", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com"+url, s.Tag(ext.HTTPURL))
	assert.Equal(nil, s.Tag(ext.Error))
	assert.Equal("bar", s.Tag("foo"))
	assert.Equal(ext.SpanKindServer, s.Tag(ext.SpanKind))
	assert.Equal("net/http", s.Tag(ext.Component))
}

func TestHttpTracer500(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// Send and verify a 500 request
	url := "/500"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router().ServeHTTP(w, r)

	assert := assert.New(t)
	assert.Equal(500, w.Code)
	assert.Equal("500!\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("GET "+url, s.Tag(ext.ResourceName))
	assert.Equal("500", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com"+url, s.Tag(ext.HTTPURL))
	assert.Equal("500: Internal Server Error", s.Tag(ext.Error).(error).Error())
	assert.Equal("bar", s.Tag("foo"))
	assert.Equal(ext.SpanKindServer, s.Tag(ext.SpanKind))
	assert.Equal("net/http", s.Tag(ext.Component))
}

func TestWrapHandler200(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	assert := assert.New(t)

	handler := WrapHandler(http.HandlerFunc(handler200), "my-service", "my-resource",
		WithSpanOptions(tracer.Tag("foo", "bar")),
	)

	url := "/"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assert.Equal(200, w.Code)
	assert.Equal("OK\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("my-resource", s.Tag(ext.ResourceName))
	assert.Equal("200", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com"+url, s.Tag(ext.HTTPURL))
	assert.Equal(nil, s.Tag(ext.Error))
	assert.Equal("bar", s.Tag("foo"))
	assert.Equal(ext.SpanKindServer, s.Tag(ext.SpanKind))
	assert.Equal("net/http", s.Tag(ext.Component))
}

func TestNoStack(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	assert := assert.New(t)

	handler := WrapHandler(http.HandlerFunc(handler500), "my-service", "my-resource",
		NoDebugStack())

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assert.Equal(http.StatusInternalServerError, w.Code)
	assert.Equal("500!\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))
	s := spans[0]
	assert.EqualError(spans[0].Tags()[ext.Error].(error), "500: Internal Server Error")
	assert.Equal("<debug stack disabled>", s.Tags()[ext.ErrorStack])
	assert.Equal(ext.SpanKindServer, s.Tag(ext.SpanKind))
	assert.Equal("net/http", s.Tag(ext.Component))
}

func TestServeMuxUsesResourceNamer(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	url := "/200"
	r := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()

	resourceNamer := func(_ *http.Request) string {
		return "custom-resource-name"
	}

	router(WithResourceNamer(resourceNamer)).ServeHTTP(w, r)

	assert := assert.New(t)
	assert.Equal(200, w.Code)
	assert.Equal("OK\n", w.Body.String())

	spans := mt.FinishedSpans()
	assert.Equal(1, len(spans))

	s := spans[0]
	assert.Equal("http.request", s.OperationName())
	assert.Equal("my-service", s.Tag(ext.ServiceName))
	assert.Equal("custom-resource-name", s.Tag(ext.ResourceName))
	assert.Equal("200", s.Tag(ext.HTTPCode))
	assert.Equal("GET", s.Tag(ext.HTTPMethod))
	assert.Equal("http://example.com"+url, s.Tag(ext.HTTPURL))
	assert.Equal(nil, s.Tag(ext.Error))
	assert.Equal("bar", s.Tag("foo"))
	assert.Equal(ext.SpanKindServer, s.Tag(ext.SpanKind))
	assert.Equal("net/http", s.Tag(ext.Component))
}

func TestServeMuxGo122Patterns(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// A mux with go1.21 patterns ("/bar") and go1.22 patterns ("GET /foo")
	mux := NewServeMux()
	mux.HandleFunc("/bar", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("GET /foo", func(w http.ResponseWriter, r *http.Request) {})

	// Try to hit both routes
	barW := httptest.NewRecorder()
	mux.ServeHTTP(barW, httptest.NewRequest("GET", "/bar", nil))
	fooW := httptest.NewRecorder()
	mux.ServeHTTP(fooW, httptest.NewRequest("GET", "/foo", nil))

	// Assert the number of spans
	assert := assert.New(t)
	spans := mt.FinishedSpans()
	assert.Equal(2, len(spans))

	// Check the /bar span
	barSpan := spans[0]
	assert.Equal(http.StatusOK, barW.Code)
	assert.Equal("/bar", barSpan.Tag(ext.HTTPRoute))
	assert.Equal("GET /bar", barSpan.Tag(ext.ResourceName))

	// Check the /foo span
	fooSpan := spans[1]
	if fooW.Code == http.StatusOK {
		assert.Equal("/foo", fooSpan.Tag(ext.HTTPRoute))
		assert.Equal("GET /foo", fooSpan.Tag(ext.ResourceName))
	} else {
		// Until our go.mod version is go1.22 or greater, the mux will not
		// understand the "GET /foo" pattern, causing the request to be handled
		// by the 404 handler. Let's assert what we can, and mark the test as
		// skipped to highlight the issue.
		assert.Equal(http.StatusNotFound, fooW.Code)
		assert.Equal(nil, fooSpan.Tag(ext.HTTPRoute))
		// Using "GET " as a resource name doesn't seem ideal, but that's how
		// the mux instrumentation deals with 404s right now.
		assert.Equal("GET ", fooSpan.Tag(ext.ResourceName))
		t.Skip("run `go mod edit -go=1.22` to run the full test")
	}

}

func TestWrapHandlerWithResourceNameNoRace(_ *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	r := httptest.NewRequest("GET", "/", nil)
	resourceNamer := func(_ *http.Request) string {
		return "custom-resource-name"
	}
	h := WrapHandler(http.NotFoundHandler(), "svc", "resc", WithResourceNamer(resourceNamer))
	mux := http.NewServeMux()
	mux.Handle("/", h)

	wg := sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			w := httptest.NewRecorder()
			defer wg.Done()
			mux.ServeHTTP(w, r)
		}()
	}
	wg.Wait()
}

func TestServeMuxNoRace(_ *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	r := httptest.NewRequest("GET", "/", nil)
	mux := NewServeMux()
	mux.Handle("/", http.NotFoundHandler())

	wg := sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			w := httptest.NewRecorder()
			defer wg.Done()
			mux.ServeHTTP(w, r)
		}()
	}
	wg.Wait()
}

func TestAnalyticsSettings(t *testing.T) {
	tests := map[string]func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option){
		"ServeMux": func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
			mux := NewServeMux(opts...)
			mux.HandleFunc("/200", handler200)
			r := httptest.NewRequest("GET", "/200", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			spans := mt.FinishedSpans()
			assert.Len(t, spans, 1)
			s := spans[0]
			assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
		},
		"WrapHandler": func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
			f := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				message := "Hello \n"
				w.Write([]byte(message))
			})
			handler := WrapHandler(f, "my-service", "my-resource", opts...)
			r := httptest.NewRequest("GET", "/200", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			spans := mt.FinishedSpans()
			assert.Len(t, spans, 1)
			s := spans[0]
			assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
		},
	}

	for name, test := range tests {
		t.Run("defaults/"+name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			test(t, mt, nil)
		})

		t.Run("global/"+name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			rate := globalconfig.AnalyticsRate()
			defer globalconfig.SetAnalyticsRate(rate)
			globalconfig.SetAnalyticsRate(0.4)

			test(t, mt, 0.4)
		})

		t.Run("enabled/"+name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			test(t, mt, 1.0, WithAnalytics(true))
		})

		t.Run("disabled/"+name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			test(t, mt, nil, WithAnalytics(false))
		})

		t.Run("override/"+name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			rate := globalconfig.AnalyticsRate()
			defer globalconfig.SetAnalyticsRate(rate)
			globalconfig.SetAnalyticsRate(0.4)

			test(t, mt, 0.23, WithAnalyticsRate(0.23))
		})
	}
}

func TestIgnoreRequestOption(t *testing.T) {
	tests := []struct {
		url       string
		spanCount int
	}{
		{
			url:       "/skip",
			spanCount: 0,
		},
		{
			url:       "/200",
			spanCount: 1,
		},
	}
	ignore := func(req *http.Request) bool {
		return req.URL.Path == "/skip"
	}
	mux := NewServeMux(WithIgnoreRequest(ignore))
	mux.HandleFunc("/skip", handler200)
	mux.HandleFunc("/200", handler200)

	for _, test := range tests {
		t.Run("servemux"+test.url, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			r := httptest.NewRequest("GET", "http://localhost"+test.url, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			spans := mt.FinishedSpans()
			assert.Equal(t, test.spanCount, len(spans))
		})

		t.Run("wraphandler"+test.url, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			r := httptest.NewRequest("GET", "http://localhost"+test.url, nil)
			w := httptest.NewRecorder()
			f := http.HandlerFunc(handler200)
			handler := WrapHandler(f, "my-service", "my-resource", WithIgnoreRequest(ignore))
			handler.ServeHTTP(w, r)

			spans := mt.FinishedSpans()
			assert.Equal(t, test.spanCount, len(spans))
		})
	}
}

func TestServerNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := NewServeMux(opts...)
		mux.HandleFunc("/200", handler200)
		r := httptest.NewRequest("GET", "http://localhost/200", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)

		return mt.FinishedSpans()
	})
	namingschematest.NewHTTPServerTest(genSpans, "http.router")(t)
}

func router(muxOpts ...Option) http.Handler {
	defaultOpts := []Option{
		WithServiceName("my-service"),
		WithSpanOptions(tracer.Tag("foo", "bar")),
	}
	mux := NewServeMux(append(defaultOpts, muxOpts...)...)
	mux.HandleFunc("/200", handler200)
	mux.HandleFunc("/500", handler500)
	return mux
}

func handler200(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("OK\n"))
}

func handler500(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "500!", http.StatusInternalServerError)
}

func BenchmarkHttpServeTrace(b *testing.B) {
	tracer.Start(tracer.WithLogger(log.DiscardLogger{}))
	defer tracer.Stop()
	header, tag := normalizer.HeaderTag("3header")
	globalconfig.SetHeaderTag(header, tag)

	r := httptest.NewRequest("GET", "/200", nil)
	r.Header.Set("h!e@a-d.e*r", "val")
	r.Header.Add("h!e@a-d.e*r", "val2")
	r.Header.Set("2header", "2val")
	r.Header.Set("3header", "some much bigger header value that you could possibly use")
	r.Header.Set("Accept", "application/json")
	r.Header.Set("User-Agent", "2val")
	r.Header.Set("Accept-Charset", "utf-8")
	r.Header.Set("Accept-Encoding", "gzip, deflate")
	r.Header.Set("Cache-Control", "no-cache")

	w := httptest.NewRecorder()
	rtr := router()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rtr.ServeHTTP(w, r)
	}
}
