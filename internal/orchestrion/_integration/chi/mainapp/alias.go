// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under another name than chi, which the rules must handle the way
// orchestrion does.
import (
	chiv4 "github.com/go-chi/chi"
)

func serveAliased() {
	r := chiv4.NewRouter()
	r.Get("/v4/alias", ok)
	serve(r, "/v4/alias")
}
