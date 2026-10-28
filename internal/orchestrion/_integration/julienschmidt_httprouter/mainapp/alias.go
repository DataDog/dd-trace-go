// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under another name than httprouter.
import (
	router "github.com/julienschmidt/httprouter"
)

func serveAliased() {
	r := router.New()
	r.GET("/main/alias", ok)
	serve(r, "/main/alias")
}
