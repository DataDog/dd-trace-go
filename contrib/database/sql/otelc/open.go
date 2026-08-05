// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"bytes"
	"database/sql"
	"runtime"
	"strconv"
	"sync"

	sqltrace "github.com/DataDog/dd-trace-go/contrib/database/sql/v2"
	"go.opentelemetry.io/otelc/pkg/hook"
)

// openGuard tracks, per goroutine, whether a BeforeOpen call is already in
// flight. Unlike OpenDB, sqltrace.Open's recursive call to database/sql.Open
// (used to discover an unregistered driver) carries the exact same
// driverName and dataSourceName as the outer call, so there is no argument
// to distinguish it by. The recursion is synchronous on the same goroutine,
// so a goroutine-scoped guard is enough, and it never blocks unrelated
// concurrent Open calls on other goroutines.
var openGuard sync.Map // goroutine ID (uint64) -> struct{}

// openResult carries sqltrace.Open's result from BeforeOpen to AfterOpen via
// HookContext.SetData/GetData. See the otelc-returnVals note in hooks.go.
type openResult struct {
	db  *sql.DB
	err error
}

// BeforeOpen mirrors the sql.Open replace-function aspect.
func BeforeOpen(ictx hook.HookContext, driverName, dataSourceName string) {
	gid := goroutineID()
	if _, reentrant := openGuard.Load(gid); reentrant {
		return
	}
	openGuard.Store(gid, struct{}{})
	defer openGuard.Delete(gid)

	db, err := sqltrace.Open(driverName, dataSourceName)
	ictx.SetData(openResult{db: db, err: err})
	ictx.SetSkipCall(true)
}

// AfterOpen writes the result BeforeOpen computed into the skipped call's
// actual return values. It is a no-op for the recursive call that BeforeOpen
// let through unmodified (IsSkipCall is false there).
func AfterOpen(ictx hook.HookContext, _ *sql.DB, _ error) {
	if !ictx.IsSkipCall() {
		return
	}
	res := ictx.GetData().(openResult)
	ictx.SetReturnVal(0, res.db)
	ictx.SetReturnVal(1, res.err)
}

// goroutineID extracts the calling goroutine's ID from the header line of
// its own stack trace ("goroutine N [running]:"). runtime.Stack is the only
// stdlib-exposed way to obtain it; it exists solely to scope openGuard.
func goroutineID() uint64 {
	var buf [128]byte
	n := runtime.Stack(buf[:], false)
	b := bytes.TrimPrefix(buf[:n], []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i >= 0 {
		b = b[:i]
	}
	id, _ := strconv.ParseUint(string(b), 10, 64)
	return id
}
