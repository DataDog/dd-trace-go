package echo_test

import (
	echotrace "github.com/DataDog/dd-trace-go/contrib/labstack/echo"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/labstack/echo"
)

// To start tracing requests, add the trace middleware to your echo router.
func Example() {
	// Create a echo.Enechoe
	r := echo.New()

	// Use the tracer middleware with your desired service name.
	r.Use(echotrace.Middleware("my-web-app"))

	// Set up some endpoints.
	r.GET("/hello", func(c echo.Context) error {
		return c.String(200, "hello world!")
	})

	// And start gathering request traces.
	r.Start(":8080")
}

func Example_spanFromContext() {
	r := echo.New()
	r.Use(echotrace.Middleware("image-encoder"))
	r.GET("/image/encode", func(c echo.Context) error {
		// create a child span to track operation timing.
		encodeSpan := tracer.NewChildSpanFromContext("image.encode", c.Request().Context())
		// encode a image
		encodeSpan.Finish()

		return c.String(200, "ok!")
	})
}
