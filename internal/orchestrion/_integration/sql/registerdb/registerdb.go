// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package registerdb registers sqlite3 from its own init(), under a name and a
// Go type of its own, as a third-party driver does. It imports nothing from
// dd-trace-go, so Go can initialize it before the tracer.
package registerdb

import (
	"database/sql"

	"github.com/mattn/go-sqlite3"
)

const Name = "sqlite3-register"

type Driver struct {
	sqlite3.SQLiteDriver
}

func init() {
	sql.Register(Name, &Driver{})
}
