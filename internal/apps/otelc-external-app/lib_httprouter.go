// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"net/http"
	"net/http/httptest"

	"github.com/julienschmidt/httprouter"
)

func init() {
	registerLibrary("httprouter", func() {
		httprouter.New().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})
}
