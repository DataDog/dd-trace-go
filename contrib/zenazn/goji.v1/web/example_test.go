// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package web_test

import (
	"fmt"
	"net/http"

	"github.com/zenazn/goji"
	"github.com/zenazn/goji.v1/web"
	webtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/zenazn/goji.v1/web"
)

func ExampleMiddleware() {
	// Using the Router middleware lets the tracer determine routes for
	// use in a trace's resource name ("GET /user/:id")
	// Otherwise the resource is only the method ("GET", "POST", etc.)
	goji.Use(goji.DefaultMux.Router)
	goji.Use(webtrace.Middleware())
	goji.Get("/hello", func(c web.C, w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Why hello there!")
	})
	goji.Serve()
}
