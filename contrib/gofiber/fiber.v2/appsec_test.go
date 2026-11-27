// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fiber

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func newAppSecRouter() *fiber.App {
	router := fiber.New()
	Wrap(router)
	router.All("/", func(c *fiber.Ctx) error {
		return c.SendString("Hello World!\n")
	})
	router.All("/params/:myPathParam", func(c *fiber.Ctx) error {
		return c.SendString("Hello World!\n")
	})
	router.All("/body", func(c *fiber.Ctx) error {
		var body struct {
			Name string `json:"name"`
		}
		if err := c.BodyParser(&body); err != nil {
			return err
		}
		// The error is deliberately ignored so that the test also covers a
		// blocking response replacing one the handler already produced.
		appsec.MonitorParsedHTTPBody(c.UserContext(), body)
		return c.SendString("Hello Body!\n")
	})
	return router
}

func TestAppSec(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "true")
	testutils.StartAppSec(t)

	router := newAppSecRouter()

	// An LFI attack in the request URI (appsec rule crs-930-110).
	t.Run("request-uri", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/../../../secret.txt", nil)
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		event, ok := spans[0].Tag("_dd.appsec.json").(string)
		require.True(t, ok, "expected an appsec event on the request span")
		require.Contains(t, event, "server.request.uri.raw")
		require.Contains(t, event, "crs-930-110")
	})

	// A security scanner attack in a route parameter (appsec rule crs-913-120).
	// Wrap installs a guard that reports parameters after route matching,
	// before the user handler runs.
	t.Run("path-params", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/params/appscan_fingerprint", nil)
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		event, ok := spans[0].Tag("_dd.appsec.json").(string)
		require.True(t, ok, "expected an appsec event on the request span")
		require.Contains(t, event, "crs-913-120")
		require.Contains(t, event, "server.request.path_params")
		require.Contains(t, event, "myPathParam")
	})

	// The same attack with Middleware on the endpoint only, without Wrap's
	// guards: the middleware reports the parameters of its endpoint.
	t.Run("path-params endpoint middleware", func(t *testing.T) {
		app := fiber.New()
		app.Post("/params/:myPathParam", Middleware(), func(c *fiber.Ctx) error { return c.SendString("ok") })
		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := app.Test(httptest.NewRequest("POST", "/params/appscan_fingerprint", nil))
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		event, ok := spans[0].Tag("_dd.appsec.json").(string)
		require.True(t, ok, "expected an appsec event on the request span")
		require.Contains(t, event, "crs-913-120")
		require.Contains(t, event, "myPathParam")
	})

	// A PHP injection attack in the parsed body, reported through the SDK,
	// which is only reachable if the operation made it into the user context.
	t.Run("SDK-body", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/body", strings.NewReader(`{"name":"$globals"}`))
		req.Header.Set("Content-Type", "application/json")
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello Body!\n", string(body))

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		event, ok := spans[0].Tag("_dd.appsec.json").(string)
		require.True(t, ok, "expected an appsec event on the request span")
		require.Contains(t, event, "crs-933-130")
	})

	t.Run("no-event", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := router.Test(httptest.NewRequest("GET", "/", nil))
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(body))

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Nil(t, spans[0].Tag("_dd.appsec.json"))
	})
}

func TestAppSecBlocking(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	testutils.StartAppSec(t)

	router := newAppSecRouter()

	// The blocked client IP is known before the handler runs, so the handler
	// must never be reached.
	t.Run("block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/", nil)
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "application/json", res.Header.Get("Content-Type"))
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NotContains(t, string(body), "Hello World!")

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Equal(t, "403", spans[0].Tag("http.status_code"))
		require.NotNil(t, spans[0].Tag("_dd.appsec.json"))
	})

	t.Run("block-html", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/", nil)
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		req.Header.Set("Accept", "text/html")
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "text/html", res.Header.Get("Content-Type"))
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "<!DOCTYPE html>")
		require.NotContains(t, string(body), "Hello World!")
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Equal(t, "403", spans[0].Tag("http.status_code"))
		require.NotNil(t, spans[0].Tag("_dd.appsec.json"))
	})

	// The body is only seen once the handler has run and written its own
	// response, so the blocking response has to replace it.
	t.Run("body-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/body", strings.NewReader(`{"name":"$globals"}`))
		req.Header.Set("Content-Type", "application/json")
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusForbidden, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NotContains(t, string(body), "Hello Body!")

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Equal(t, "403", spans[0].Tag("http.status_code"))
	})

	t.Run("no-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := router.Test(httptest.NewRequest("POST", "/", nil))
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(body))
	})
}

// TestAppSecNormalizedRoute checks the normalized route of fiber requests: none
// when no route handles the request, and the implicit catch-all of a prefix
// route (Use, Static).
func TestAppSecNormalizedRoute(t *testing.T) {
	t.Setenv("DD_API_SECURITY_ENABLED", "true")
	testutils.StartAppSec(t)

	static := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(static, "app.css"), []byte("body{}"), 0o600))
	routes := func(app *fiber.App) *fiber.App {
		// A middleware that renders the router's 404 itself: its route is not the
		// route of the request.
		app.Use(func(c *fiber.Ctx) error {
			err := c.Next()
			if err != nil && strings.HasPrefix(c.Path(), "/handled") {
				return c.SendStatus(http.StatusNotFound)
			}
			return err
		})
		// A middleware that only calls c.Next. On another path than the tracing
		// middleware, fiber does not merge it into the same route.
		app.Use("/nope", func(c *fiber.Ctx) error { return c.Next() })
		app.Get("/users/:id", func(c *fiber.Ctx) error { return c.SendString("ok") })
		app.Get("/missing/:id", func(*fiber.Ctx) error { return fiber.ErrNotFound })
		// An endpoint that returns the 404 of fiber's router keeps its route.
		app.Get("/fixed/:id", func(c *fiber.Ctx) error {
			return fiber.NewError(fiber.StatusNotFound, "Cannot "+c.Method()+" "+c.Path())
		})
		app.Get("/item", func(c *fiber.Ctx) error { return c.SendString("ok") })
		// A handler that returns the error of fiber's router keeps its route.
		app.Get("/refused", func(c *fiber.Ctx) error {
			c.Set(fiber.HeaderAllow, fiber.MethodPost)
			return fiber.ErrMethodNotAllowed
		})
		app.Get("/color::color", func(c *fiber.Ctx) error { return c.SendString("ok") })
		app.Get("/files/*.txt", func(c *fiber.Ctx) error { return c.SendString("ok") })
		app.Get("/a%2Fb", func(c *fiber.Ctx) error { return c.SendString("ok") })
		app.Use("/p%25", func(c *fiber.Ctx) error { return c.SendString("ok") })
		app.Use("/w/*/fixed", func(c *fiber.Ctx) error { return c.SendString("ok") })
		// A prefix handler that answers 405 itself keeps its route.
		app.Use("/readonly", func(*fiber.Ctx) error { return fiber.ErrMethodNotAllowed })
		app.Use("/api", func(c *fiber.Ctx) error { return c.SendString("ok") })
		app.Use("/pre/", func(c *fiber.Ctx) error { return c.SendString("ok") })
		app.Static("/static", static)
		return app
	}
	wrapped := fiber.New()
	Wrap(wrapped)
	middleware := fiber.New()
	middleware.Use(Middleware())
	// Middleware on an endpoint only: c.Route() is the endpoint before and after
	// the handler.
	endpoint := fiber.New()
	endpoint.Get("/users/:id", Middleware(), func(c *fiber.Ctx) error { return c.SendString("ok") })
	// UnescapePath: fiber matches the unescaped path, where the % of a route
	// is static text.
	unescaped := fiber.New(fiber.Config{UnescapePath: true})
	unescaped.Use(Middleware())
	apps := map[string]*fiber.App{"Wrap": routes(wrapped), "Middleware": routes(middleware), "Endpoint": endpoint, "UnescapePath": routes(unescaped)}

	// Rows without apps run on Wrap, Middleware and UnescapePath.
	for _, tc := range []struct {
		method, target string
		status         int
		want           string // empty: no tag
		apps           []string
	}{
		{"GET", "/nope/x", http.StatusNotFound, "", nil},
		{"GET", "/handled", http.StatusNotFound, "", nil},
		{"GET", "/readonly/x", http.StatusMethodNotAllowed, "/readonly/{param1}", nil},
		{"POST", "/users/1", http.StatusMethodNotAllowed, "", nil},
		{"GET", "/users/1", http.StatusOK, "/users/{id}", []string{"Wrap", "Middleware", "UnescapePath", "Endpoint"}},
		{"GET", "/missing/1", http.StatusNotFound, "/missing/{id}", nil},
		{"GET", "/fixed/4", http.StatusNotFound, "/fixed/{id}", nil},
		{"GET", "/refused", http.StatusMethodNotAllowed, "/refused", nil},
		{"GET", "/color:blue", http.StatusOK, "/color%3A{color}", nil},
		{"GET", "/files/report.txt", http.StatusOK, "/files/{param1}.txt", nil},
		// Without StrictRouting, fiber matches "/item" with "/item///".
		{"GET", "/item///", http.StatusOK, "/item", nil},
		{"GET", "/api/v1/x", http.StatusOK, "/api/{param1}", nil},
		{"GET", "/pre//x", http.StatusOK, "/pre/{param1}", nil},
		{"GET", "/w/x/fixed/rest", http.StatusOK, "/w/{param1}/fixed/{param2}", nil},
		{"GET", "/static/app.css", http.StatusOK, "/static/{param1}", nil},
		// fiber matches the escaped path, or the unescaped path with UnescapePath.
		{"GET", "/a%2Fb", http.StatusOK, "/a%2Fb", []string{"Wrap", "Middleware"}},
		{"GET", "/a%252Fb", http.StatusOK, "/a%252Fb", []string{"UnescapePath"}},
		{"GET", "/p%2525/x", http.StatusOK, "/p%2525/{param1}", []string{"UnescapePath"}},
	} {
		names := tc.apps
		if names == nil {
			names = []string{"Wrap", "Middleware", "UnescapePath"}
		}
		for _, name := range names {
			t.Run(name+" "+tc.method+" "+tc.target, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()

				res, err := apps[name].Test(httptest.NewRequest(tc.method, tc.target, nil))
				require.NoError(t, err)
				defer res.Body.Close()
				require.Equal(t, tc.status, res.StatusCode)
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				if tc.want == "" {
					require.Nil(t, spans[0].Tag("_dd.appsec.normalized_route"))
				} else {
					require.Equal(t, tc.want, spans[0].Tag("_dd.appsec.normalized_route"))
				}
			})
		}
	}
}

// TestAppSecNormalizedRouteBlocked checks that a blocked request gets the
// normalized route that fiber matched, with UnescapePath: when a route guard of
// Wrap blocks on the path parameters, and when the AppSec SDK blocks in the
// handler, on routes with and without parameters.
func TestAppSecNormalizedRouteBlocked(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	t.Setenv("DD_API_SECURITY_ENABLED", "true")
	testutils.StartAppSec(t)

	blockBody := func(c *fiber.Ctx) error {
		if err := appsec.MonitorParsedHTTPBody(c.UserContext(), map[string]string{"name": "$globals"}); err != nil {
			return err
		}
		return c.SendString("ok")
	}
	routes := func(app *fiber.App) *fiber.App {
		app.Get("/a%2Fb/:id", func(c *fiber.Ctx) error { return c.SendString("ok") })
		app.Get("/body/a%2Fb", blockBody)
		app.Get("/body/a%2Fb/:id", blockBody)
		return app
	}
	wrapped := fiber.New(fiber.Config{UnescapePath: true})
	Wrap(wrapped)
	middleware := fiber.New(fiber.Config{UnescapePath: true})
	middleware.Use(Middleware())
	apps := map[string]*fiber.App{"Wrap": routes(wrapped), "Middleware": routes(middleware)}

	for _, tc := range []struct{ app, target, want string }{
		{"Wrap", "/a%252Fb/$globals", "/a%252Fb/{id}"},
		{"Wrap", "/body/a%252Fb", "/body/a%252Fb"},
		{"Wrap", "/body/a%252Fb/1", "/body/a%252Fb/{id}"},
		{"Middleware", "/body/a%252Fb", "/body/a%252Fb"},
		{"Middleware", "/body/a%252Fb/1", "/body/a%252Fb/{id}"},
		{"Middleware", "/body/a%252Fb/%3Cscript%3E", "/body/a%252Fb/{id}"},
	} {
		t.Run(tc.app+" "+tc.target, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			res, err := apps[tc.app].Test(httptest.NewRequest("GET", tc.target, nil))
			require.NoError(t, err)
			defer res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode)
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			require.Equal(t, tc.want, spans[0].Tag("_dd.appsec.normalized_route"))
			if tc.app == "Middleware" {
				// No WAF evaluation after the block: the path parameters would
				// match crs-941-110.
				require.NotContains(t, spans[0].Tag("_dd.appsec.json"), "crs-941-110")
			}
		})
	}
}
