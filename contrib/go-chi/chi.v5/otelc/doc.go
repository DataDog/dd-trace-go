// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules for
// github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2.
//
// Separate module so blank-importing it from otelc/all does not pull go-chi/chi/v5
// or the contrib into the go.mod of every otelc-enabled application.
package otelc
