package echo

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/labstack/echo"
)

// To start tracing requests, add the trace middleware to your echo router.
func Example() {
	// Create a echo.Enechoe
	r := echo.New()

	// Use the tracer middleware with your desired service name.
	r.Use(Middleware("my-web-app"))

	// Set up some endpoints.
	r.GET("/hello", func(c echo.Context) error {
		return c.String(200, "hello world!")
	})

	// And start gathering request traces.
	r.Start(":8080")
}

// Trace a some operation as child of parent span
func Example_spanFromContext() {
	// Create a echo.Enechoe
	r := echo.New()

	// Use the tracer middleware with your desired service name.
	r.Use(Middleware("image-encoder"))

	// Set up some endpoints.
	r.GET("/image/encode", func(c echo.Context) error {
		// create a child span to track operation timing.
		span, _ := tracer.StartSpanFromContext(c.Request().Context(), "image.encode")

		// encode a image ...

		// finish a child span
		span.Finish()

		return c.String(200, "ok!")
	})
}
