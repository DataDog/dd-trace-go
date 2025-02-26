// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo_test

import (
	"encoding/json"
	"io"
	"net/http"

	echotrace "github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/v2"
	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/labstack/echo/v4"
)

// To start tracing requests, add the trace middleware to your echo router.
func Example() {
	tracer.Start()
	defer tracer.Stop()

	r := echo.New()

	// Use the tracer middleware with your desired service name.
	r.Use(echotrace.Middleware(echotrace.WithService("my-web-app")))

	// Set up an endpoint.
	r.GET("/hello", func(c echo.Context) error {
		return c.String(200, "hello world!")
	})

	// ...and listen for incoming requests
	r.Start(":8080")
}

// An example illustrating tracing a child operation within the main context.
func Example_spanFromContext() {
	tracer.Start()
	defer tracer.Stop()

	// Create a new instance of echo
	r := echo.New()

	// Use the tracer middleware with your desired service name.
	r.Use(echotrace.Middleware(echotrace.WithService("image-encoder")))

	// Set up some endpoints.
	r.GET("/image/encode", func(c echo.Context) error {
		// create a child span to track an operation
		span, _ := tracer.StartSpanFromContext(c.Request().Context(), "image.encode")

		// encode an image ...

		// finish the child span
		span.Finish()

		return c.String(200, "ok!")
	})
}

type parsedBodyType struct {
	Value string `json:"value"`
}

func customBodyParser(body io.ReadCloser) (*parsedBodyType, error) {
	var parsedBody parsedBodyType
	err := json.NewDecoder(body).Decode(&parsedBody)
	return &parsedBody, err
}

// Monitor HTTP request parsed body
func ExampleMonitorParsedHTTPBody() {
	tracer.Start()
	defer tracer.Stop()

	mux := httptrace.NewServeMux()
	mux.HandleFunc("/body", func(w http.ResponseWriter, r *http.Request) {
		// Use the SDK to monitor the request's parsed body
		body, err := customBodyParser(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		appsec.MonitorParsedHTTPBody(r.Context(), body)
		w.Write([]byte("Body monitored using AppSec SDK\n"))
	})
	http.ListenAndServe(":8080", mux)
}

// Monitor HTTP request parsed body with a framework customized context type
func ExampleMonitorParsedHTTPBody_customContext() {
	tracer.Start()
	defer tracer.Stop()

	r := echo.New()
	r.Use(echotrace.Middleware())
	r.POST("/body", func(c echo.Context) (e error) {
		req := c.Request()
		body, err := customBodyParser(req.Body)
		if err != nil {
			return c.String(http.StatusInternalServerError, err.Error())
		}
		// Use the SDK to monitor the request's parsed body
		appsec.MonitorParsedHTTPBody(c.Request().Context(), body)
		return c.String(http.StatusOK, "Body monitored using AppSec SDK")
	})

	r.Start(":8080")
}

func userIDFromRequest(r *http.Request) string {
	return r.Header.Get("user-id")
}

// Monitor and block requests depending on user ID
func ExampleSetUser() {
	tracer.Start()
	defer tracer.Stop()

	mux := httptrace.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		// We use SetUser() here to associate the user ID to the request's span. The return value
		// can then be checked to decide whether to block the request or not.
		// If it should be blocked, early exit from the handler.
		if err := appsec.SetUser(r.Context(), userIDFromRequest(r)); err != nil {
			return
		}

		w.Write([]byte("User monitored using AppSec SetUser SDK\n"))
	})
}
