// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package restful

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/emicklei/go-restful/v3"

	restfultrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/emicklei/go-restful.v3"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type Integration struct {
	ws       *restful.WebService
	numSpans int
	opts     []restfultrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]restfultrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "emicklei/go-restful.v3"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()
	i.ws = new(restful.WebService)
	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()
	assert := assert.New(t)

	i.ws.Filter(restfultrace.FilterFunc(i.opts...))
	i.ws.Route(i.ws.GET("/user/{id}").Param(restful.PathParameter("id", "user ID")).
		To(func(request *restful.Request, response *restful.Response) {
			_, ok := tracer.SpanFromContext(request.Request.Context())
			assert.True(ok)
			id := request.PathParameter("id")
			response.Write([]byte(id))
		}))

	container := restful.NewContainer()
	container.Add(i.ws)

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	container.ServeHTTP(w, r)
	response := w.Result()
	assert.Equal(response.StatusCode, 200)
	i.numSpans++

	wantErr := errors.New("oh no")

	i.ws.Filter(restfultrace.FilterFunc(i.opts...))
	i.ws.Route(i.ws.GET("/err").To(func(request *restful.Request, response *restful.Response) {
		response.WriteError(500, wantErr)
	}))

	container = restful.NewContainer()
	container.Add(i.ws)

	r = httptest.NewRequest("GET", "/err", nil)
	w = httptest.NewRecorder()

	container.ServeHTTP(w, r)
	w.Result()
	i.numSpans += 2
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, restfultrace.WithServiceName(name))
}
