// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules for go-chi/chi.v5. See otelc.yaml.
package otelc

import (
	_ "github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2" // used by the otelc.yaml rules
)
