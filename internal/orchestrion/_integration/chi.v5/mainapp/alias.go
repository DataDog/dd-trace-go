// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under another name than chi, which the rules must handle the way
// orchestrion does.
import (
	router "github.com/go-chi/chi/v5"
)

func serveAliased() {
	r := router.NewRouter()
	r.Get("/v5/alias", ok)
	serve(r, "/v5/alias")
}
