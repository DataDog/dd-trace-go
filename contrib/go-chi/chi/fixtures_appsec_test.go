// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
)

// routerFixture is a router setup. build gets the middleware that registers the
// probe operation (it must be the first middleware of the root router), and the
// handler of the endpoint.
type routerFixture struct {
	name   string
	method string
	target string // the request target; "ATTACK" is replaced by the value of the path parameter
	build  func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler
}

// opaqueHandler hides the router behind a handler wrapper. chi.Routes.Match
// cannot follow a mount to such a handler.
func opaqueHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) })
}

func okHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

// rewriteOld changes the route path from "/old/<x>" to "/files/<x>" (the path of
// the request does not change).
func rewriteOld(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil && strings.HasPrefix(r.URL.Path, "/old/") {
			rctx.RoutePath = "/files/" + strings.TrimPrefix(r.URL.Path, "/old/")
		}
		next.ServeHTTP(w, r)
	})
}

// replaceContext replaces the request context, but keeps the chi route context.
func replaceContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(context.Background(), chi.RouteCtxKey, chi.RouteContext(r.Context()))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// rewriteParent rewrites the route path of "/old/<x>" to the clean path
// "/api/files/clean".
func rewriteParent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil && strings.HasPrefix(r.URL.Path, "/old/") {
			rctx.RoutePath = "/api/files/clean"
		}
		next.ServeHTTP(w, r)
	})
}

func fixtureRootUse() routerFixture {
	return routerFixture{"1 root Use", "GET", "/users/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, Middleware())
		r.Get("/users/{id}", h)
		return r
	}}
}

func fixtureSubRouterUse() routerFixture {
	return routerFixture{"2 Use in a sub-router", "GET", "/api/users/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first)
		r.Route("/api", func(sr chi.Router) {
			sr.Use(Middleware())
			sr.Get("/users/{id}", h)
		})
		return r
	}}
}

func fixtureWith() routerFixture {
	return routerFixture{"3 With", "GET", "/users/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first)
		r.With(Middleware()).Get("/users/{id}", h)
		return r
	}}
}

func fixtureStripSlashes(catchAll bool) routerFixture {
	name := "4 StripSlashes after the tracer"
	if catchAll {
		name = "4b StripSlashes after the tracer, with a /* route"
	}
	return routerFixture{name, "GET", "/users/ATTACK/", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, Middleware(), middleware.StripSlashes)
		r.Get("/users/{id}", h)
		if catchAll {
			r.HandleFunc("/*", okHandler)
		}
		return r
	}}
}

func fixtureGetHead() routerFixture {
	return routerFixture{"5 GetHead after the tracer", "HEAD", "/users/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, Middleware(), middleware.GetHead)
		r.Get("/users/{id}", h)
		return r
	}}
}

func fixtureDirectMount() routerFixture {
	return routerFixture{"6 direct mount", "GET", "/api/users/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, Middleware())
		sub := chi.NewRouter()
		sub.Get("/users/{id}", h)
		r.Mount("/api", sub)
		return r
	}}
}

func fixtureWrappedMount() routerFixture {
	return routerFixture{"7 wrapped mount", "GET", "/api/users/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, Middleware())
		sub := chi.NewRouter()
		sub.Get("/users/{id}", h)
		r.Mount("/api", opaqueHandler(sub))
		return r
	}}
}

// fixtureRewrite is setup 8 (routes /files/{name} and /*), or 8i (/files/{id}).
func fixtureRewrite(param string) routerFixture {
	name := "8 rewrite after the tracer"
	if param == "id" {
		name = "8i rewrite after the tracer, id"
	}
	return routerFixture{name, "GET", "/old/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, Middleware(), rewriteOld)
		r.Get("/files/{"+param+"}", h)
		r.HandleFunc("/*", okHandler)
		return r
	}}
}

func fixtureTwoTracers() routerFixture {
	return routerFixture{"9 root Use and Use in a mounted router", "GET", "/api/users/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, Middleware())
		child := chi.NewRouter()
		child.Use(Middleware())
		child.Get("/users/{id}", h)
		r.Mount("/api", child)
		return r
	}}
}

func fixtureWithMount() routerFixture {
	return routerFixture{"10 With(Middleware()).Mount", "GET", "/api/users/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first)
		sub := chi.NewRouter()
		sub.Get("/users/{id}", h)
		r.With(Middleware()).Mount("/api", sub)
		return r
	}}
}

func fixtureLiteralLeaf() routerFixture {
	return routerFixture{"11 literal leaf in a mount", "GET", "/api/attack", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, Middleware())
		child := chi.NewRouter()
		child.Get("/attack", h)
		r.Mount("/api", child)
		return r
	}}
}

func fixtureReplacedContext() routerFixture {
	return routerFixture{"12 context replaced after the tracer", "GET", "/users/ATTACK/", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, Middleware(), replaceContext, middleware.StripSlashes)
		r.Get("/users/{id}", h)
		return r
	}}
}

func fixtureParentRewrite() routerFixture {
	return routerFixture{"13 parent rewrites before a sub-router tracer", "GET", "/old/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, rewriteParent)
		child := chi.NewRouter()
		child.Use(Middleware())
		child.Get("/files/{id}", h)
		r.Mount("/api", child)
		r.HandleFunc("/*", okHandler)
		return r
	}}
}

// fixtureWrappedEndpoint is setup 14: root Use, a wrapped mount, and a tracer on
// the endpoint of the child router.
func fixtureWrappedEndpoint() routerFixture {
	return routerFixture{"14 wrapped mount, tracer on the endpoint", "GET", "/api/users/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, Middleware())
		sub := chi.NewRouter()
		sub.With(Middleware()).Get("/users/{id}", h)
		r.Mount("/api", opaqueHandler(sub))
		return r
	}}
}

// rewriteToNamedMount rewrites the route path of "/old/<x>" to
// "/api/<x>/items/clean": the parent routes it to a mount with a parameter.
func rewriteToNamedMount(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil && strings.HasPrefix(r.URL.Path, "/old/") {
			rctx.RoutePath = "/api/" + strings.TrimPrefix(r.URL.Path, "/old/") + "/items/clean"
		}
		next.ServeHTTP(w, r)
	})
}

// fixtureNamedMountRewrite is setup 15: the parent rewrites the route path to a
// mount with a parameter, and the child router has the tracer. chi knows the
// parameter "id" of the mount when the tracer runs, but the URL of the request
// matches the catch-all route of the parent.
func fixtureNamedMountRewrite() routerFixture {
	return routerFixture{"15 parent rewrites to a mount with a parameter", "GET", "/old/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, rewriteToNamedMount)
		child := chi.NewRouter()
		child.Use(Middleware())
		child.Get("/items/{item}", h)
		r.Mount("/api/{id}", child)
		r.HandleFunc("/*", okHandler)
		return r
	}}
}

// fixtureOrdinaryMount is a mount of a handler that is not a chi router.
func fixtureOrdinaryMount() routerFixture {
	return routerFixture{"16 ordinary handler in a mount", "GET", "/files/ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first, Middleware())
		r.Mount("/files", h)
		return r
	}}
}

// rewriteSamePattern changes the route path of "/api/<x>/items/clean?id=<y>" to
// "/api/<y>/items/clean": the parent routes the same mount pattern with another
// value of the parameter.
func rewriteSamePattern(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil && r.URL.Query().Get("id") != "" {
			rctx.RoutePath = "/api/" + r.URL.Query().Get("id") + "/items/clean"
		}
		next.ServeHTTP(w, r)
	})
}

// fixtureSamePatternRewrite is setup 17: the parent rewrites the route path so
// that the mount "/api/{id}" gets another "id" than the URL of the request. The
// lookup of the URL gives the same patterns, with the other value.
func fixtureSamePatternRewrite(rootTracer bool) routerFixture {
	name := "17 parent rewrites the value of a mount parameter"
	if rootTracer {
		name = "17b as 17, with a tracer on the root router"
	}
	return routerFixture{name, "GET", "/api/clean/items/clean?id=ATTACK", func(first func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
		r := chi.NewRouter()
		r.Use(first)
		if rootTracer {
			r.Use(Middleware())
		}
		r.Use(rewriteSamePattern)
		child := chi.NewRouter()
		child.Use(Middleware())
		child.Get("/items/{item}", h)
		r.Mount("/api/{id}", child)
		return r
	}}
}
