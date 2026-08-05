// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc hooks for the gorm.io/gorm.v1 integration. It
// is its own module so that go.opentelemetry.io/otelc/pkg/hook does not leak
// into the go.mod of everyone importing the contrib.
package otelc

import (
	"gorm.io/gorm"

	gormtrace "github.com/DataDog/dd-trace-go/contrib/gorm.io/gorm.v1/v2"

	"go.opentelemetry.io/otelc/pkg/hook"
)

// AfterOpen registers the tracing plugin on a *gorm.DB just opened through
// gorm.Open, unless one is already registered. Guards against double
// registration from gormtrace.Open, which calls gorm.Open internally and
// wires the same callbacks itself.
//
// gorm.Open can return a non-nil *gorm.DB alongside a non-nil error (a
// partially initialized connection). On error this drops that value, the
// same as ../orchestrion.yml's Open aspect.
func AfterOpen(ictx hook.HookContext, db *gorm.DB, err error) {
	if err != nil {
		ictx.SetReturnVal(0, (*gorm.DB)(nil))
		return
	}

	plugin := gormtrace.NewTracePlugin()
	if _, ok := db.Plugins[plugin.Name()]; ok {
		return
	}
	if useErr := db.Use(plugin); useErr != nil {
		ictx.SetReturnVal(0, (*gorm.DB)(nil))
		ictx.SetReturnVal(1, useErr)
	}
}
