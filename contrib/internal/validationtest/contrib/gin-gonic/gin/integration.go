// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gin

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	gintrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gin-gonic/gin"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type Integration struct {
	router   *gin.Engine
	numSpans int
	opts     []gintrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]gintrace.Option, 0),
	}
}

func (i *Integration) ResetNumSpans() {
	i.numSpans = 0
}

func (i *Integration) Name() string {
	return "gin-gonic/gin"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode) // silence annoying log msgs

	i.router = gin.New()
	i.router.Use(gintrace.Middleware("", i.opts...))
	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()
	assert := assert.New(t)

	i.router.GET("/user/:id", func(c *gin.Context) {
		_, ok := tracer.SpanFromContext(c.Request.Context())
		assert.True(ok)
	})

	r := httptest.NewRequest("GET", "/user/123", nil)
	w := httptest.NewRecorder()

	i.router.ServeHTTP(w, r)
	i.numSpans++

	i.router.GET("/user2/:id", func(c *gin.Context) {
		_, ok := tracer.SpanFromContext(c.Request.Context())
		assert.True(ok)
		id := c.Param("id")
		c.Writer.Write([]byte(id))
	})

	r = httptest.NewRequest("GET", "/user2/123", nil)
	w = httptest.NewRecorder()

	// do and verify the request
	i.router.ServeHTTP(w, r)
	response := w.Result()
	defer response.Body.Close()
	assert.Equal(response.StatusCode, 200)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	return
}
