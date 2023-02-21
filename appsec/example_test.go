// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package appsec_test

import (
	"encoding/json"
	"io"
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec"
	echotrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/labstack/echo.v4"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"

	"github.com/labstack/echo/v4"
)

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
