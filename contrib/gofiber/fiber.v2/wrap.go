// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fiber

import (
	"runtime"
	"slices"
	"sync"
	"weak"

	"github.com/gofiber/fiber/v2"
)

// Both keys and values are weak: an option may itself refer to the app. The
// registry must not keep apps alive after their servers and handlers are gone.
var wrappedApps sync.Map // map[weak.Pointer[fiber.App]]weak.Pointer[config]

type wrappedRequestKey struct{}

// Wrap installs tracing and AppSec route guards and returns app. Call it before
// registering middleware, routes, or mounted apps, and before serving requests.
// Wrap each mounted app as well. The first wrapped app reached by a request
// owns its span and options; mounted apps do not add another span. If its
// WithIgnoreRequest option skips a request, mounted apps also skip monitoring.
//
// Route guards inspect Fiber's matched path parameters before calling that
// route's user handlers. Global Middleware alone cannot provide this protection.
//
// Repeated calls apply additional options without installing duplicate handlers.
// This lets explicit options override Orchestrion's automatic setup. All calls
// must happen during setup, not while the app serves requests. Calls on the same
// app must not run concurrently. Complete route registration before serving.
// Wrap panics if the first call occurs after middleware, routes, or mounts have
// been registered. Under Orchestrion, the first call happens at fiber.New.
func Wrap(app *fiber.App, opts ...Option) *fiber.App {
	key := weak.Make(app)
	if value, ok := wrappedApps.Load(key); ok {
		if cfg := value.(weak.Pointer[config]).Value(); cfg != nil {
			for _, opt := range opts {
				opt.apply(cfg)
			}
			return app
		}
	}
	if len(app.GetRoutes()) != 0 {
		panic("fibertrace.Wrap must be called before registering middleware, routes, or mounts")
	}
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	wrappedApps.Store(key, weak.Make(cfg))
	runtime.AddCleanup(app, func(key weak.Pointer[fiber.App]) { wrappedApps.Delete(key) }, key)

	handler := middleware(cfg)
	app.Use(func(c *fiber.Ctx) error {
		if c.Locals(wrappedRequestKey{}) != nil {
			return c.Next()
		}
		c.Locals(wrappedRequestKey{}, true)
		defer c.Locals(wrappedRequestKey{}, nil)
		return handler(c)
	})

	// Fiber calls OnRoute under its setup lock, after appending or merging the
	// route. The hook's copy may refer to a different handler slice, and its
	// method may still be USE after a middleware merge. Inspect the actual last
	// route of each method stack; only newly appended handlers need guards.
	guarded := make(map[*fiber.Route]int)
	app.Hooks().OnRoute(func(fiber.Route) error {
		for _, stack := range app.Stack() {
			if len(stack) == 0 {
				continue
			}
			route := stack[len(stack)-1]
			start := guarded[route]
			if start == len(route.Handlers) {
				continue
			}
			// Get, All, and mounted routes can share a handler slice. Do not
			// change another route's handlers when installing these guards.
			handlers := slices.Clone(route.Handlers)
			for i := start; i < len(handlers); i++ {
				handlers[i] = guardRouteParams(handlers[i])
			}
			route.Handlers = handlers
			guarded[route] = len(handlers)
		}
		return nil
	})
	return app
}
