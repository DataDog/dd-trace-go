package twirp

import (
	"context"
	"net/http"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/stretchr/testify/assert"
	"github.com/twitchtv/twirp"
	"github.com/twitchtv/twirp/ctxsetters"
)

func TestServerHooks(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	hooks := NewServerHooks(WithServiceName("twirp-test"), WithAnalytics(true))
	server := func(assert *assert.Assertions, twerr twirp.Error) {
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

	t.Run("success", func(t *testing.T) {
		defer mt.Reset()
		assert := assert.New(t)

		server(assert, nil)

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("twirp-test", span.Tag(ext.ServiceName))
		assert.Equal("twirp.request", span.OperationName())
		assert.Equal("Method", span.Tag(ext.ResourceName))
		assert.Equal("200", span.Tag(ext.HTTPCode))
	})

	t.Run("error", func(t *testing.T) {
		defer mt.Reset()
		assert := assert.New(t)

		server(assert, twirp.InternalError("something bad or unexpected happened"))

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("twirp-test", span.Tag(ext.ServiceName))
		assert.Equal("twirp.request", span.OperationName())
		assert.Equal("Method", span.Tag(ext.ResourceName))
		assert.Equal("500", span.Tag(ext.HTTPCode))
		assert.Equal("twirp error internal: something bad or unexpected happened", span.Tag(ext.Error).(error).Error())
	})
}
