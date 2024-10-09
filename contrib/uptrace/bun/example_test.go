// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package bun_test

import (
	"context"
	"database/sql"

	buntrace "github.com/DataDog/dd-trace-go/contrib/uptrace/bun/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

func Example() {
	tracer.Start()
	defer tracer.Stop()

	sqlite, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		panic(err)
	}
	db := bun.NewDB(sqlite, sqlitedialect.New())

	// Wrap the connection with the APM hook.
	buntrace.Wrap(db)
	var user struct {
		Name string
	}
	_ = db.NewSelect().Column("name").Table("users").Scan(context.Background(), &user)
}
