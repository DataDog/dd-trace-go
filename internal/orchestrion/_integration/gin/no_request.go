// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseNoRequest struct{}

func (*TestCaseNoRequest) Setup(context.Context, *testing.T) {
	gin.SetMode(gin.ReleaseMode)
}

func (*TestCaseNoRequest) Run(_ context.Context, t *testing.T) {
	renderWithoutRequest(t)
}

func (*TestCaseNoRequest) ExpectedTraces() trace.Traces {
	return nil
}

func (*TestCaseNoRequest) ExpectedSpanCount() int {
	return 0
}

func renderWithoutRequest(t *testing.T) {
	t.Helper()
	payload := gin.H{"message": "ok"}
	cases := []struct {
		name   string
		render func(*gin.Context)
		body   string
	}{
		{"JSON", func(c *gin.Context) { c.JSON(http.StatusCreated, payload) }, `{"message":"ok"}`},
		{"AsciiJSON", func(c *gin.Context) { c.AsciiJSON(http.StatusCreated, payload) }, `{"message":"ok"}`},
		{"IndentedJSON", func(c *gin.Context) { c.IndentedJSON(http.StatusCreated, payload) }, "{\n    \"message\": \"ok\"\n}"},
		{"JSONP", func(c *gin.Context) { c.JSONP(http.StatusCreated, payload) }, `{"message":"ok"}`},
		{"PureJSON", func(c *gin.Context) { c.PureJSON(http.StatusCreated, payload) }, "{\"message\":\"ok\"}\n"},
		{"SecureJSON", func(c *gin.Context) { c.SecureJSON(http.StatusCreated, payload) }, `{"message":"ok"}`},
		{"XML", func(c *gin.Context) { c.XML(http.StatusCreated, payload) }, "<map><message>ok</message></map>"},
		{"YAML", func(c *gin.Context) { c.YAML(http.StatusCreated, payload) }, "message: ok\n"},
		{"TOML", func(c *gin.Context) { c.TOML(http.StatusCreated, payload) }, "message = 'ok'\n"},
		{"ProtoBuf", func(c *gin.Context) { c.ProtoBuf(http.StatusCreated, wrapperspb.String("ok")) }, "\x0a\x02ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			// Gin supports rendering directly on its requestless public test context.
			require.Nil(t, c.Request)
			require.NotPanics(t, func() { tc.render(c) })
			require.Equal(t, http.StatusCreated, recorder.Code)
			require.Equal(t, tc.body, recorder.Body.String())
		})
	}
}
