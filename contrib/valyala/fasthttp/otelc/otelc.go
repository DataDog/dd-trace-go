// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules for valyala/fasthttp. See otelc.yaml.
package otelc

import (
	// Links in the contrib, which the declaration in link.go points at.
	_ "github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2"
)
