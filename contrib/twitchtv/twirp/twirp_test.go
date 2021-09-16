// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package twirp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
	"github.com/twitchtv/twirp"
	"github.com/twitchtv/twirp/ctxsetters"
	"github.com/twitchtv/twirp/example"
)

type mockClient struct {
	code int
	err  error
}

func (mc *mockClient) Do(req *http.Request) (*http.Response, error) {
	if mc.err != nil {
		return nil, mc.err
	}
	// the request body in a response should be nil based on the documentation of http.Response
	req.Body = nil
	res := &http.Response{
		Status:     fmt.Sprintf("%d %s", mc.code, http.StatusText(mc.code)),
		StatusCode: mc.code,
		Proto:      req.Proto,
		ProtoMajor: req.ProtoMajor,
		ProtoMinor: req.ProtoMinor,
		Request:    req,
	}
	return res, nil
}

func TestClient(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	ctx := context.Background()
	ctx = ctxsetters.WithPackageName(ctx, "twirp.test")
	ctx = ctxsetters.WithServiceName(ctx, "Example")
	ctx = ctxsetters.WithMethodName(ctx, "Method")

	url := "http://localhost/twirp/twirp.test/Example/Method"

	t.Run("success", func(t *testing.T) {
		defer mt.Reset()
		assert := assert.New(t)

		mc := &mockClient{code: 200}
		wc := WrapClient(mc)

		req, err := http.NewRequest("POST", url, nil)
		assert.NoError(err)
		req = req.WithContext(ctx)

		_, err = wc.Do(req)
		assert.NoError(err)

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal(ext.SpanTypeHTTP, span.Tag(ext.SpanType))
		assert.Equal("twirp.request", span.OperationName())
		assert.Equal("twirp.request", span.Tag(ext.ResourceName))
		assert.Equal("twirp.test", span.Tag("twirp.package"))
		assert.Equal("Example", span.Tag("twirp.service"))
		assert.Equal("Method", span.Tag("twirp.method"))
		assert.Equal("200", span.Tag(ext.HTTPCode))
	})

	t.Run("server-error", func(t *testing.T) {
		defer mt.Reset()
		assert := assert.New(t)

		mc := &mockClient{code: 500}
		wc := WrapClient(mc)

		req, err := http.NewRequest("POST", url, nil)
		assert.NoError(err)
		req = req.WithContext(ctx)

		_, err = wc.Do(req)
		assert.NoError(err)

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal(ext.SpanTypeHTTP, span.Tag(ext.SpanType))
		assert.Equal("twirp.request", span.OperationName())
		assert.Equal("twirp.request", span.Tag(ext.ResourceName))
		assert.Equal("twirp.test", span.Tag("twirp.package"))
		assert.Equal("Example", span.Tag("twirp.service"))
		assert.Equal("Method", span.Tag("twirp.method"))
		assert.Equal("500", span.Tag(ext.HTTPCode))
		assert.Equal(true, span.Tag(ext.Error).(bool))
	})

	t.Run("timeout", func(t *testing.T) {
		defer mt.Reset()
		assert := assert.New(t)

		mc := &mockClient{err: context.DeadlineExceeded}
		wc := WrapClient(mc)

		req, err := http.NewRequest("POST", url, nil)
		assert.NoError(err)
		req = req.WithContext(ctx)

		_, err = wc.Do(req)
		assert.Equal(context.DeadlineExceeded, err)

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal(ext.SpanTypeHTTP, span.Tag(ext.SpanType))
		assert.Equal("twirp.request", span.OperationName())
		assert.Equal("twirp.request", span.Tag(ext.ResourceName))
		assert.Equal("twirp.test", span.Tag("twirp.package"))
		assert.Equal("Example", span.Tag("twirp.service"))
		assert.Equal("Method", span.Tag("twirp.method"))
		assert.Equal(context.DeadlineExceeded, span.Tag(ext.Error))
	})
}

func mockServer(hooks *twirp.ServerHooks, assert *assert.Assertions, twerr twirp.Error) {
	ctx := context.Background()
	ctx = ctxsetters.WithPackageName(ctx, "twirp.test")
	ctx = ctxsetters.WithServiceName(ctx, "Example")
	ctx, err := hooks.RequestReceived(ctx)
	assert.NoError(err)

	ctx = ctxsetters.WithMethodName(ctx, "Method")
	ctx, err = hooks.RequestRouted(ctx)
	assert.NoError(err)

	if twerr != nil {
		ctx = ctxsetters.WithStatusCode(ctx, twirp.ServerHTTPStatusFromErrorCode(twerr.Code()))
		ctx = hooks.Error(ctx, twerr)
	} else {
		ctx = hooks.ResponsePrepared(ctx)
		ctx = ctxsetters.WithStatusCode(ctx, http.StatusOK)
	}

	hooks.ResponseSent(ctx)
}

func TestServerHooks(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	hooks := NewServerHooks(WithServiceName("twirp-test"), WithAnalytics(true))

	t.Run("success", func(t *testing.T) {
		defer mt.Reset()
		assert := assert.New(t)

		mockServer(hooks, assert, nil)

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("twirp-test", span.Tag(ext.ServiceName))
		assert.Equal("twirp.Example", span.OperationName())
		assert.Equal("twirp.test", span.Tag("twirp.package"))
		assert.Equal("Example", span.Tag("twirp.service"))
		assert.Equal("Method", span.Tag("twirp.method"))
		assert.Equal("200", span.Tag(ext.HTTPCode))
	})

	t.Run("error", func(t *testing.T) {
		defer mt.Reset()
		assert := assert.New(t)

		mockServer(hooks, assert, twirp.InternalError("something bad or unexpected happened"))

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("twirp-test", span.Tag(ext.ServiceName))
		assert.Equal("twirp.Example", span.OperationName())
		assert.Equal("twirp.test", span.Tag("twirp.package"))
		assert.Equal("Example", span.Tag("twirp.service"))
		assert.Equal("Method", span.Tag("twirp.method"))
		assert.Equal("500", span.Tag(ext.HTTPCode))
		assert.Equal("twirp error internal: something bad or unexpected happened", span.Tag(ext.Error).(error).Error())
	})

	t.Run("chained", func(t *testing.T) {
		defer mt.Reset()
		assert := assert.New(t)

		otherHooks := &twirp.ServerHooks{
			RequestReceived: func(ctx context.Context) (context.Context, error) {
				_, ctx = tracer.StartSpanFromContext(ctx, "other.span.name")
				return ctx, nil
			},
			ResponseSent: func(ctx context.Context) {
				span, ok := tracer.SpanFromContext(ctx)
				if !ok {
					return
				}
				span.Finish()
			},
		}
		mockServer(twirp.ChainHooks(hooks, otherHooks), assert, twirp.InternalError("something bad or unexpected happened"))

		spans := mt.FinishedSpans()
		assert.Len(spans, 2)
		span := spans[0]
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("twirp-test", span.Tag(ext.ServiceName))
		assert.Equal("twirp.Example", span.OperationName())
		assert.Equal("twirp.test", span.Tag("twirp.package"))
		assert.Equal("Example", span.Tag("twirp.service"))
		assert.Equal("Method", span.Tag("twirp.method"))
		assert.Equal("500", span.Tag(ext.HTTPCode))
		assert.Equal("twirp error internal: something bad or unexpected happened", span.Tag(ext.Error).(error).Error())

		span = spans[1]
		assert.Equal("other.span.name", span.OperationName())
	})
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		hooks := NewServerHooks(opts...)
		assert := assert.New(t)
		mockServer(hooks, assert, nil)

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		s := spans[0]
		assert.Equal(rate, s.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
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

func TestServiceNameSettings(t *testing.T) {
	assertServiceName := func(t *testing.T, mt mocktracer.Tracer, serviceName string, opts ...Option) {
		hooks := NewServerHooks(opts...)
		assert := assert.New(t)
		mockServer(hooks, assert, nil)

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		s := spans[0]
		assert.Equal(serviceName, s.Tag(ext.ServiceName))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertServiceName(t, mt, "twirp-server")
	})

	t.Run("global", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		svc := globalconfig.ServiceName()
		defer globalconfig.SetServiceName(svc)
		globalconfig.SetServiceName("service.global")

		assertServiceName(t, mt, "service.global")
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		svc := globalconfig.ServiceName()
		defer globalconfig.SetServiceName(svc)
		globalconfig.SetServiceName("service.global")

		assertServiceName(t, mt, "service.local", WithServiceName("service.local"))
	})
}

type notifyListener struct {
	net.Listener
	ch chan<- struct{}
}

func (n *notifyListener) Accept() (c net.Conn, err error) {
	if n.ch != nil {
		close(n.ch)
		n.ch = nil
	}
	return n.Listener.Accept()
}

type haberdasher int32

func (h haberdasher) MakeHat(ctx context.Context, size *example.Size) (*example.Hat, error) {
	if size.Inches != int32(h) {
		return nil, twirp.InvalidArgumentError("Inches", "Only size of %d is allowed")
	}
	hat := &example.Hat{
		Size:  size.Inches,
		Color: "purple",
		Name:  "doggie beanie",
	}
	return hat, nil
}

func TestHaberdash(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	assert := assert.New(t)

	l, err := net.Listen("tcp4", "127.0.0.1:0")
	assert.NoError(err)
	defer l.Close()

	readyCh := make(chan struct{})
	nl := &notifyListener{Listener: l, ch: readyCh}

	server := WrapServer(example.NewHaberdasherServer(haberdasher(6), NewServerHooks()))
	errCh := make(chan error)
	go func() {
		err := http.Serve(nl, server)
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-readyCh:
		break
	case err := <-errCh:
		assert.FailNow("server not started", err)
	}

	client := example.NewHaberdasherJSONClient("http://"+nl.Addr().String(), WrapClient(&http.Client{}))
	hat, err := client.MakeHat(context.Background(), &example.Size{Inches: 6})
	assert.NoError(err)
	assert.Equal("purple", hat.Color)

	spans := mt.FinishedSpans()
	assert.Len(spans, 3)
	assert.Equal(ext.SpanTypeWeb, spans[0].Tag(ext.SpanType))
	assert.Equal(ext.SpanTypeWeb, spans[1].Tag(ext.SpanType))
	assert.Equal(ext.SpanTypeHTTP, spans[2].Tag(ext.SpanType))
}
