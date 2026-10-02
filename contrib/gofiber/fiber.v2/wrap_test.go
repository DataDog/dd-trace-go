// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fiber

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func startWrapAppSec(t *testing.T) {
	t.Helper()
	// These tests check routing, not WAF timeouts under the race detector.
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	testutils.StartAppSec(t)
}

func TestWrapPathBlocking(t *testing.T) {
	startWrapAppSec(t)
	for _, tc := range []struct {
		name, prefix, route string
		setup               func(*fiber.App, fiber.Handler, fiber.Handler)
		allowedCalls        int32
	}{
		{"simple", "/params/", "/params/:value", func(app *fiber.App, _ fiber.Handler, handler fiber.Handler) {
			app.Post("/params/:value", handler)
		}, 1},
		{"multiple-handlers", "/params/", "/params/:value", func(app *fiber.App, next, handler fiber.Handler) {
			app.Post("/params/:value", next, handler)
		}, 2},
		{"merged-routes", "/params/", "/params/:value", func(app *fiber.App, next, handler fiber.Handler) {
			app.Post("/params/:value", next)
			app.Post("/params/:value", handler)
		}, 2},
		{"separate-routes", "/params/", "/params/:value", func(app *fiber.App, next, handler fiber.Handler) {
			app.Post("/params/:value", next)
			app.Post("/unrelated", handler)
			app.Post("/params/:value", handler)
		}, 2},
		{"group", "/api/params/", "/api/params/:value", func(app *fiber.App, _ fiber.Handler, handler fiber.Handler) {
			app.Group("/api").Post("/params/:value", handler)
		}, 1},
		{"wildcard", "/files/", "/files/*", func(app *fiber.App, _ fiber.Handler, handler fiber.Handler) {
			app.Post("/files/*", handler)
		}, 1},
		{"optional", "/params/", "/params/:value?", func(app *fiber.App, _ fiber.Handler, handler fiber.Handler) {
			app.Post("/params/:value?", handler)
		}, 1},
		{"mounted", "/child/params/", "/child/params/:value", func(app *fiber.App, _ fiber.Handler, handler fiber.Handler) {
			child := Wrap(fiber.New())
			child.Post("/params/:value", handler)
			app.Mount("/child", child)
		}, 1},
		{"registered-after-mount", "/child/params/", "/child/params/:value", func(app *fiber.App, _ fiber.Handler, handler fiber.Handler) {
			child := Wrap(fiber.New())
			app.Mount("/child", child)
			child.Post("/params/:value", handler)
		}, 1},
		{"nested-mount", "/child/nested/params/", "/child/nested/params/:value", func(app *fiber.App, _ fiber.Handler, handler fiber.Handler) {
			child, nested := Wrap(fiber.New()), Wrap(fiber.New())
			nested.Post("/params/:value", handler)
			child.Mount("/nested", nested)
			app.Mount("/child", child)
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var calls atomic.Int32
			app := Wrap(fiber.New())
			tc.setup(app, func(c *fiber.Ctx) error {
				calls.Add(1)
				return c.Next()
			}, func(c *fiber.Ctx) error {
				calls.Add(1)
				return c.SendString("allowed")
			})
			res, err := app.Test(httptest.NewRequest("POST", tc.prefix+"$globals", nil))
			require.NoError(t, err)
			body, err := io.ReadAll(res.Body)
			require.NoError(t, res.Body.Close())
			require.NoError(t, err)
			require.Equal(t, http.StatusForbidden, res.StatusCode)
			require.Zero(t, calls.Load(), "path blocking must happen before user code")
			require.Contains(t, string(body), "You've been blocked")
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1, "mounted apps must not add another span")
			require.Equal(t, tc.route, spans[0].Tag("http.route"))
			require.Equal(t, "403", spans[0].Tag("http.status_code"))
			event, ok := spans[0].Tag("_dd.appsec.json").(string)
			require.True(t, ok)
			require.Contains(t, event, "server.request.path_params")
			require.Contains(t, event, "crs-933-130-block")

			mt.Reset()
			res, err = app.Test(httptest.NewRequest("POST", tc.prefix+"benign", nil))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, tc.allowedCalls, calls.Load())
			spans = mt.FinishedSpans()
			require.Len(t, spans, 1)
			require.Nil(t, spans[0].Tag("_dd.appsec.json"))
		})
	}
}

func TestWrapAllMethods(t *testing.T) {
	startWrapAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var calls atomic.Int32
	app := Wrap(fiber.New())
	app.All("/params/:value", func(c *fiber.Ctx) error {
		calls.Add(1)
		return c.SendString("allowed")
	})
	for _, method := range app.Config().RequestMethods {
		res, err := app.Test(httptest.NewRequest(method, "/params/$globals", nil))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusForbidden, res.StatusCode, method)
		require.Zero(t, calls.Load(), method)
		res, err = app.Test(httptest.NewRequest(method, "/params/benign", nil))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode, method)
		require.Equal(t, int32(1), calls.Swap(0), method)
	}
}

func TestWrapReusesUnchangedParams(t *testing.T) {
	startWrapAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	app := Wrap(fiber.New())
	var first, last string
	app.Get("/params/:value", func(c *fiber.Ctx) error {
		// The cached map is replaced when parameters are reported to the WAF.
		first = fmt.Sprintf("%p", c.Locals(appsecRequestKey{}).(*appsecRequest).params)
		return c.Next()
	}, func(c *fiber.Ctx) error {
		last = fmt.Sprintf("%p", c.Locals(appsecRequestKey{}).(*appsecRequest).params)
		return c.SendString("allowed")
	})
	res, err := app.Test(httptest.NewRequest("GET", "/params/benign", nil))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NotEqual(t, "0x0", first)
	require.Equal(t, first, last, "unchanged parameters must not cause another WAF run")
}

func TestWrapParameterizedGroupMiddleware(t *testing.T) {
	startWrapAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var groupCalls, handlerCalls atomic.Int32
	app := Wrap(fiber.New())
	group := app.Group("/groups/:group", func(c *fiber.Ctx) error {
		groupCalls.Add(1)
		return c.Next()
	})
	group.Get("/items/:item", func(c *fiber.Ctx) error {
		handlerCalls.Add(1)
		return c.SendString("allowed")
	})
	for _, tc := range []struct {
		path, route string
		groupCalls  int32
	}{
		{"/groups/$globals/items/benign", "/groups/:group", 0},
		{"/groups/benign/items/$globals", "/groups/:group/items/:item", 1},
	} {
		res, err := app.Test(httptest.NewRequest("GET", tc.path, nil))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Zero(t, handlerCalls.Load())
		require.Equal(t, tc.groupCalls, groupCalls.Load(), tc.path)
		spans := mt.FinishedSpans()
		require.Equal(t, tc.route, spans[len(spans)-1].Tag("http.route"))
	}
	require.Equal(t, int32(1), groupCalls.Load(), "the group handler may run only with benign group parameters")
}

func TestWrapIgnoredBlockCannotEnterNextHandler(t *testing.T) {
	startWrapAppSec(t)
	for _, static := range []bool{false, true} {
		t.Run(fmt.Sprintf("static-%t", static), func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var calls atomic.Int32
			app := Wrap(fiber.New())
			app.Use(func(c *fiber.Ctx) error {
				_ = c.Next()
				return c.Next()
			})
			handler := func(c *fiber.Ctx) error {
				calls.Add(1)
				return c.SendString("must not run")
			}
			if static {
				app.Use("/params/:value", handler)
				app.Get("/params/$globals", handler)
			} else {
				app.Get("/params/:value", handler, handler)
			}
			res, err := app.Test(httptest.NewRequest("GET", "/params/$globals", nil))
			require.NoError(t, err)
			defer res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode)
			require.Zero(t, calls.Load())
		})
	}
}

func TestWrapUsesFiberRouting(t *testing.T) {
	startWrapAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var calls atomic.Int32
	cfg := fiber.Config{
		CaseSensitive:  true,
		StrictRouting:  true,
		RequestMethods: []string{"GET", "HEAD", "PURGE"},
	}
	app := Wrap(fiber.New(cfg))
	control := fiber.New(cfg)
	control.Add("PURGE", "/Params/:value/", func(c *fiber.Ctx) error { return c.SendString("allowed") })
	app.Add("PURGE", "/Params/:value/", func(c *fiber.Ctx) error {
		calls.Add(1)
		return c.SendString("allowed")
	})
	for _, path := range []string{"/Params/$globals/", "/Params/benign/", "/params/$globals/", "/Params/$globals"} {
		// Let Fiber define which parameterized routes match, including trailing
		// slash behavior. Only matched attacks should change from 200 to 403.
		res, err := control.Test(httptest.NewRequest("PURGE", path, nil))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		status := res.StatusCode
		if status == http.StatusOK && strings.Contains(path, "$globals") {
			status = http.StatusForbidden
		}
		res, err = app.Test(httptest.NewRequest("PURGE", path, nil))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, status, res.StatusCode, path)
	}
	require.Equal(t, int32(1), calls.Load())
}

func TestWrapRestartRouting(t *testing.T) {
	startWrapAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var calls atomic.Int32
	app := Wrap(fiber.New())
	app.Get("/params/:value", func(c *fiber.Ctx) error {
		calls.Add(1)
		if c.Params("value") == "benignxx" {
			c.Path("/params/$globals")
			return c.RestartRouting()
		}
		return c.SendString("must not run")
	})
	res, err := app.Test(httptest.NewRequest("GET", "/params/benignxx", nil))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, int32(1), calls.Load())
	require.Len(t, mt.FinishedSpans(), 1)
}

func TestWrapRepeatedSetup(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	app := fiber.New()
	require.Same(t, app, Wrap(app))
	app.Get("/params/:value", func(c *fiber.Ctx) error { return c.SendString("allowed") })
	handlers := app.HandlersCount()
	routes := len(app.GetRoutes())
	Wrap(app, WithService("custom"))
	Wrap(app)
	require.Equal(t, handlers, app.HandlersCount())
	require.Len(t, app.GetRoutes(), routes)
	res, err := app.Test(httptest.NewRequest("GET", "/params/benign", nil))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "custom", spans[0].Tag("service.name"))
}

func TestWrapRejectsLateSetup(t *testing.T) {
	for _, setup := range []func(*fiber.App){
		func(app *fiber.App) { app.Get("/", func(c *fiber.Ctx) error { return c.SendString("allowed") }) },
		func(app *fiber.App) { app.Use(func(c *fiber.Ctx) error { return c.Next() }) },
		func(app *fiber.App) { app.Mount("/child", fiber.New()) },
	} {
		app := fiber.New()
		setup(app)
		require.PanicsWithValue(t, "fibertrace.Wrap must be called before registering middleware, routes, or mounts", func() { Wrap(app) })
	}
}

func TestWrapIgnoredAndDisabled(t *testing.T) {
	for _, ignored := range []bool{false, true} {
		name := "disabled"
		if ignored {
			name = "ignored"
		}
		t.Run(name, func(t *testing.T) {
			if ignored {
				startWrapAppSec(t)
			}
			mt := mocktracer.Start()
			defer mt.Stop()
			app := Wrap(fiber.New(), WithIgnoreRequest(func(*fiber.Ctx) bool { return ignored }))
			child := Wrap(fiber.New())
			child.Get("/params/:value", func(c *fiber.Ctx) error { return c.SendString("allowed") })
			app.Mount("/child", child)
			res, err := app.Test(httptest.NewRequest("GET", "/child/params/$globals", nil))
			require.NoError(t, err)
			defer res.Body.Close()
			require.Equal(t, http.StatusOK, res.StatusCode)
			if ignored {
				require.Empty(t, mt.FinishedSpans())
			} else {
				require.Len(t, mt.FinishedSpans(), 1)
			}
		})
	}
}

func TestWrapConcurrentRequests(t *testing.T) {
	startWrapAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var calls atomic.Int32
	app := Wrap(fiber.New())
	app.Get("/params/:value", func(c *fiber.Ctx) error {
		calls.Add(1)
		return c.SendString("allowed")
	})
	app.Handler()
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for i := range 16 {
		wg.Go(func() {
			value, status := "benign", http.StatusOK
			if i%2 == 0 {
				value, status = "$globals", http.StatusForbidden
			}
			res, err := app.Test(httptest.NewRequest("GET", "/params/"+value, nil), 10_000)
			if err != nil {
				failures <- err
				return
			}
			res.Body.Close()
			if res.StatusCode != status {
				failures <- fmt.Errorf("expected status %d, got %d", status, res.StatusCode)
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.Equal(t, int32(8), calls.Load())
	require.Len(t, mt.FinishedSpans(), 16)
}

func TestWrapDoesNotRetainApps(t *testing.T) {
	key := func() weak.Pointer[fiber.App] {
		app := fiber.New()
		Wrap(app, WithResourceNamer(func(*fiber.Ctx) string { return app.Config().AppName }))
		return weak.Make(app)
	}()
	require.Eventually(t, func() bool {
		runtime.GC()
		_, present := wrappedApps.Load(key)
		return key.Value() == nil && !present
	}, 5*time.Second, 10*time.Millisecond)
}
