// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import "net/http"

// These handlers are used in tests from code_origin_test.go. They are moved to a separate file so line numbers
// are as stable as possible.

func testHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Hello World"))
}

type CustomHandler struct{}

func (h *CustomHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	testHandler(w, r)
}
