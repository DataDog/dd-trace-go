// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under another name than echo, which the rules must handle the way
// orchestrion does.
import (
	web "github.com/labstack/echo/v4"
)

func serveAliased() {
	e := web.New()
	e.GET("/v4/alias", ok)
	serve(e, "/v4/alias")
}
