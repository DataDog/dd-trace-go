// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fiber

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v2"
	fiberrecover "github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

// This negative control shows why recovery outside tracing is unsupported for
// AppSec response inspection. The application's recovery still owns the panic.
func TestPanicRecoveryOutsideTracing(t *testing.T) {
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	t.Setenv("DD_APPSEC_RULES", "testdata/response-blocking.json")
	testutils.StartAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var recovered any
	var renderedError error
	router := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		renderedError = err
		c.Set("X-Panic-Recovered", "yes")
		return c.Status(http.StatusInternalServerError).SendString("panic response")
	}})
	router.Use(fiberrecover.New(fiberrecover.Config{
		EnableStackTrace:  true,
		StackTraceHandler: func(_ *fiber.Ctx, value any) { recovered = value },
	}))
	router.Use(Middleware())
	router.Get("/panic/:value", func(*fiber.Ctx) error { panic("handler panic") })
	res, err := router.Test(httptest.NewRequest("GET", "/panic/benign", nil), 10_000)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	require.Equal(t, "handler panic", recovered)
	require.EqualError(t, renderedError, "handler panic")
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	require.Nil(t, spans[0].Tag("http.status_code"))
	require.Nil(t, spans[0].Tag("_dd.appsec.json"), "the response rule cannot run after the operation has finished")
}

func TestPanicRecoveryOrdering(t *testing.T) {
	for _, setup := range []string{"wrap", "middleware"} {
		for _, tc := range []struct {
			name, rules, rule  string
			enabled, bodyBlock bool
			status, spanStatus int
			errorCalls         int32
		}{
			{"reported", "../../../internal/appsec/testdata/blocking.json", "", true, false, 500, 500, 1},
			{"response-blocked", "testdata/response-blocking.json", "fiber-block-panic-response", true, false, 403, 403, 1},
			{"body-blocked", "../../../internal/appsec/testdata/blocking.json", "crs-933-130-block", true, true, 403, 403, 0},
			// Without AppSec, Fiber still renders errors after tracing returns.
			{"disabled", "", "", false, false, 500, 200, 1},
		} {
			t.Run(setup+"/"+tc.name, func(t *testing.T) {
				if tc.enabled {
					t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
					t.Setenv("DD_APPSEC_RULES", tc.rules)
					testutils.StartAppSec(t)
				}
				mt := mocktracer.Start()
				defer mt.Stop()
				var errorCalls, recoveryCalls atomic.Int32
				var bodyBlocked atomic.Bool
				wantPanic := fiber.NewError(http.StatusInternalServerError, "handler panic")
				var recovered any
				var renderedError error
				router := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
					errorCalls.Add(1)
					renderedError = err
					c.Set("X-Panic-Recovered", "yes")
					c.Cookie(&fiber.Cookie{Name: "recovery", Value: "must-not-escape"})
					return c.Status(http.StatusInternalServerError).SendString("panic response")
				}})
				if setup == "wrap" {
					Wrap(router)
				} else {
					router.Use(Middleware())
				}
				router.Use(fiberrecover.New(fiberrecover.Config{
					EnableStackTrace: true,
					StackTraceHandler: func(_ *fiber.Ctx, value any) {
						recoveryCalls.Add(1)
						recovered = value
					},
				}))
				router.Get("/panic/:value", func(c *fiber.Ctx) error {
					if tc.bodyBlock {
						bodyBlocked.Store(appsec.MonitorParsedHTTPBody(c.UserContext(), map[string]string{"attack": "$globals"}) != nil)
					}
					panic(wantPanic)
				})
				res, err := router.Test(httptest.NewRequest("GET", "/panic/benign", nil), 10_000)
				require.NoError(t, err)
				body, err := io.ReadAll(res.Body)
				require.NoError(t, res.Body.Close())
				require.NoError(t, err)
				require.Equal(t, tc.status, res.StatusCode)
				require.Equal(t, int32(1), recoveryCalls.Load(), "the application's recovery hook must run")
				require.Same(t, wantPanic, recovered)
				require.Equal(t, tc.errorCalls, errorCalls.Load())
				if tc.errorCalls != 0 {
					require.Same(t, wantPanic, renderedError)
				}
				require.Equal(t, tc.bodyBlock, bodyBlocked.Load())
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				require.Equal(t, strconv.Itoa(tc.spanStatus), spans[0].Tag("http.status_code"))
				require.Equal(t, "/panic/:value", spans[0].Tag("http.route"))
				if tc.rule != "" {
					require.True(t, json.Valid(body))
					require.Contains(t, string(body), "You've been blocked")
					require.Empty(t, res.Header.Values("Set-Cookie"))
					require.Empty(t, res.Header.Get("X-Panic-Recovered"))
					require.Nil(t, spans[0].Tag("error.message"))
					event, ok := spans[0].Tag("_dd.appsec.json").(string)
					require.True(t, ok)
					require.Contains(t, event, tc.rule)
					if !tc.bodyBlock {
						require.Contains(t, event, "server.response.status")
						require.Contains(t, event, "server.response.headers.no_cookies")
						require.Contains(t, event, "server.request.path_params")
					}
				} else {
					require.Equal(t, "panic response", string(body))
					require.Equal(t, "yes", res.Header.Get("X-Panic-Recovered"))
					require.Len(t, res.Header.Values("Set-Cookie"), 1)
					require.Equal(t, "handler panic", spans[0].Tag("error.message"))
				}
			})
		}
	}
}
