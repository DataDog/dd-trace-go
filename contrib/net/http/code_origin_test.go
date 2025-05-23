// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func testHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Hello World"))
}

type CustomHandler struct{}

func (h *CustomHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	testHandler(w, r)
}

func TestCodeOriginForSpans(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	testFilePath, err := filepath.Abs(filename)
	require.NoError(t, err)

	testCases := []struct {
		name       string
		disabled   bool
		getHandler func() http.Handler
		wantTags   map[string]any
	}{
		{
			name:     "disabled",
			disabled: true,
			getHandler: func() http.Handler {
				return http.HandlerFunc(testHandler)
			},
			wantTags: map[string]any{},
		},
		{
			name: "HandlerFunc",
			getHandler: func() http.Handler {
				return http.HandlerFunc(testHandler)
			},
			wantTags: map[string]any{
				"_dd.code_origin.type":          "entry",
				"_dd.code_origin.frames.0.file": testFilePath,
				"_dd.code_origin.frames.0.line": "23",
			},
		},
		{
			name: "NewServeMux",
			getHandler: func() http.Handler {
				mux := http.NewServeMux()
				mux.HandleFunc("/", testHandler)
				return mux
			},
			wantTags: map[string]any{
				"_dd.code_origin.type":          "entry",
				"_dd.code_origin.frames.0.file": testFilePath,
				"_dd.code_origin.frames.0.line": "23",
			},
		},
		{
			name: "CustomHandler",
			getHandler: func() http.Handler {
				return &CustomHandler{}
			},
			wantTags: map[string]any{
				"_dd.code_origin.type":          "entry",
				"_dd.code_origin.frames.0.file": testFilePath,
				"_dd.code_origin.frames.0.line": "23",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			t.Setenv("DD_CODE_ORIGIN_FOR_SPANS_ENABLED", strconv.FormatBool(!tc.disabled))
			httptrace.ResetCfg()

			h := WrapHandler(tc.getHandler(), "code-origins", "testHandler")
			url := "/"
			r := httptest.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			s0 := spans[0]
			require.Equal(t, "http.request", s0.OperationName())
			require.Equal(t, "code-origins", s0.Tag(ext.ServiceName))
			require.Equal(t, "testHandler", s0.Tag(ext.ResourceName))

			gotTags := make(map[string]any)
			for tag, value := range s0.Tags() {
				if strings.HasPrefix(tag, "_dd.code_origin") {
					gotTags[tag] = value
				}
			}
			assert.Equal(t, tc.wantTags, gotTags, "_dd.code_origin tags mismatch")
		})
	}
}
