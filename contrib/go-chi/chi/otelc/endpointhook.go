// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build ignore

package otelc

import (
	"net/http"
	_ "unsafe"
)

//go:linkname __dd_chitrace_wrapEndpoint github.com/DataDog/dd-trace-go/contrib/go-chi/chi/v2/internal/endpointhook.Wrap
func __dd_chitrace_wrapEndpoint(http.Handler, bool) http.Handler
