// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc hooks for database/sql. See otelc.yaml.
package otelc

import (
	"database/sql"
	"database/sql/driver"

	"go.opentelemetry.io/otelc/pkg/hook"

	sqltrace "github.com/DataDog/dd-trace-go/contrib/database/sql/v2"
)

func AfterRegister(ictx hook.HookContext) {
	name, _ := ictx.GetParam(0).(string)
	drv, _ := ictx.GetParam(1).(driver.Driver)
	sqltrace.Register(name, drv)
}

type openResult struct {
	db  *sql.DB
	err error
}

func BeforeOpen(ictx hook.HookContext, driverName, dataSourceName string) {
	if calledByContrib() {
		return
	}
	ictx.SetSkipCall(true)
	db, err := sqltrace.Open(driverName, dataSourceName)
	ictx.SetData(openResult{db, err})
}

func AfterOpen(ictx hook.HookContext, _ *sql.DB, _ error) {
	if r, ok := ictx.GetData().(openResult); ok {
		ictx.SetReturnVal(0, r.db)
		ictx.SetReturnVal(1, r.err)
	}
}

func BeforeOpenDB(ictx hook.HookContext, c driver.Connector) {
	if calledByContrib() {
		return
	}
	ictx.SetSkipCall(true)
	ictx.SetData(sqltrace.OpenDB(c))
}

func AfterOpenDB(ictx hook.HookContext, _ *sql.DB) {
	if db, ok := ictx.GetData().(*sql.DB); ok {
		ictx.SetReturnVal(0, db)
	}
}
