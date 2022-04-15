// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gin_test

import (
	gintrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gin-gonic/gin"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/gin-gonic/gin"
)

// To start tracing requests, add the trace middleware to your Gin router.
func Example() {
	tracer.Start()
	defer tracer.Stop()

	// Create a gin.Engine
	r := gin.New()

	// Use the tracer middleware with your desired service name.
	r.Use(gintrace.Middleware("my-web-app"))

	// Set up some endpoints.
	r.GET("/hello", func(c *gin.Context) {
		c.String(200, "hello world!")
	})

	// And start gathering request traces.
	r.Run(":8080")
}

func ExampleHTML() {
	tracer.Start()
	defer tracer.Stop()

	r := gin.Default()
	r.Use(gintrace.Middleware("my-web-app"))
	r.LoadHTMLGlob("templates/*")

	r.GET("/index", func(c *gin.Context) {
		// render the html and trace the execution time.
		gintrace.HTML(c, 200, "index.tmpl", gin.H{
			"title": "Main website",
		})
	})
}

func Example_spanFromContext() {
	tracer.Start()
	defer tracer.Stop()

	r := gin.Default()
	r.Use(gintrace.Middleware("image-encoder"))
	r.GET("/image/encode", func(c *gin.Context) {
		ctx := c.Request.Context()
		// create a child span to track operation timing.
		encodeSpan, _ := tracer.StartSpanFromContext(ctx, "image.encode")
		// encode a image
		encodeSpan.Finish()

		c.String(200, "ok!")
	})
}
