// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc hooks database/sql's Register, Open and OpenDB, the otelc
// port of the aspects in ../orchestrion.yml. Orchestrion rewrites call sites
// in application code, so sqltrace's own calls to these same stdlib
// functions are never woven by its own aspects. otelc hooks the function
// definitions instead, so sqltrace's internal calls back into
// database/sql.Open and database/sql.OpenDB re-enter this same hook;
// BeforeOpenDB below and BeforeOpen in open.go each guard against that.
//
// Open and OpenDB skip the real call and substitute sqltrace's own result.
// otelc only allocates HookContext.returnVals for an After trampoline (built
// from pointers to the target function's real return variables); a
// Before-only hook's HookContext.returnVals is never allocated, so
// SetReturnVal from a Before hook indexes into a nil slice. The computed
// result is threaded from Before to After via SetData/GetData on the same
// HookContext instead.
package otelc

import (
	"database/sql"
	"database/sql/driver"

	sqltrace "github.com/DataDog/dd-trace-go/contrib/database/sql/v2"
	"go.opentelemetry.io/otelc/pkg/hook"
)

// BeforeRegister mirrors the sql.Register aspect: the wrap-expression there
// calls both database/sql.Register and sqltrace.Register from the call
// site. This hook calls sqltrace.Register and leaves SkipCall false, so the
// real database/sql.Register still runs afterwards. No guard is needed:
// sqltrace.Register never calls back into database/sql.Register.
func BeforeRegister(_ hook.HookContext, driverName string, driver driver.Driver) {
	sqltrace.Register(driverName, driver)
}

// BeforeOpenDB mirrors the sql.OpenDB replace-function aspect. sqltrace.
// OpenDB wraps c in a *tracedConnector and passes that to the real
// database/sql.OpenDB, which re-enters this hook; IsTracedConnector
// recognizes that recursive call by the connector's type and lets it
// through unmodified instead of wrapping it again.
func BeforeOpenDB(ictx hook.HookContext, c driver.Connector) {
	if sqltrace.IsTracedConnector(c) {
		return
	}
	ictx.SetData(sqltrace.OpenDB(c))
	ictx.SetSkipCall(true)
}

// AfterOpenDB writes the *sql.DB computed by BeforeOpenDB into the skipped
// call's actual return value. It is a no-op for the recursive call that
// BeforeOpenDB let through unmodified (IsSkipCall is false there).
func AfterOpenDB(ictx hook.HookContext, _ *sql.DB) {
	if !ictx.IsSkipCall() {
		return
	}
	ictx.SetReturnVal(0, ictx.GetData().(*sql.DB))
}
