package restful

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/emicklei/go-restful"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestTrace200(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	ws := new(restful.WebService)
	ws.Filter(Filter)
	ws.Route(ws.GET("/user/{id}").Param(restful.PathParameter("id", "user ID")).
		To(func(request *restful.Request, response *restful.Response) {
			_, ok := tracer.SpanFromContext(request.Request.Context())
			assert.True(ok)
			id := request.PathParameter("id")
			response.Write([]byte(id))
		}))

	container := restful.NewContainer()
	container.Add(ws)

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	container.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Contains(span.Tag(ext.ResourceName), "/user/{id}")
	assert.Equal("200", span.Tag(ext.HTTPCode))
	assert.Equal("GET", span.Tag(ext.HTTPMethod))
	assert.Equal("/user/123", span.Tag(ext.HTTPURL))
}

func TestError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	wantErr := errors.New("oh no")

	ws := new(restful.WebService)
	ws.Filter(Filter)
	ws.Route(ws.GET("/err").To(func(request *restful.Request, response *restful.Response) {
		response.WriteError(500, wantErr)
	}))

	container := restful.NewContainer()
	container.Add(ws)

	r := httptest.NewRequest("GET", "/err", nil)
	w := httptest.NewRecorder()

	container.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 500)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("http.request", span.OperationName())
	assert.Equal("500", span.Tag(ext.HTTPCode))
	assert.Equal(wantErr.Error(), span.Tag(ext.Error).(error).Error())
}

func TestPropagation(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	pspan := tracer.StartSpan("test")
	tracer.Inject(pspan.Context(), tracer.HTTPHeadersCarrier(r.Header))

	ws := new(restful.WebService)
	ws.Filter(Filter)
	ws.Route(ws.GET("/user/{id}").To(func(request *restful.Request, response *restful.Response) {
		span, ok := tracer.SpanFromContext(request.Request.Context())
		assert.True(ok)
		assert.Equal(span.(mocktracer.Span).ParentID(), pspan.(mocktracer.Span).SpanID())
	}))

	container := restful.NewContainer()
	container.Add(ws)

	container.ServeHTTP(w, r)
}
