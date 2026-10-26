// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp calls gorm.Open from package main, where the harness suites
// never run. TestOpenInMain builds and runs it.
package main

import (
	"context"
	"fmt"
	"log"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Opened while the package initializes, before main runs. The contrib must
// already be initialized here.
var varDB, varErr = gorm.Open(sqlite.Open("file:var?mode=memory"))

func main() {
	if varErr != nil {
		log.Fatal(varErr)
	}
	query(varDB, "SELECT 'var'")

	db, err := gorm.Open(sqlite.Open("file:main?mode=memory"))
	if err != nil {
		log.Fatal(err)
	}
	query(db, "SELECT 'main'")

	queryAliased()

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func query(db *gorm.DB, q string) {
	var s string
	if err := db.WithContext(context.Background()).Raw(q).Scan(&s).Error; err != nil {
		log.Fatal(err)
	}
}
