// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"log"

	"gorm.io/driver/sqlite"
	gormv1 "gorm.io/gorm"
)

func queryAliased() {
	db, err := gormv1.Open(sqlite.Open("file:alias?mode=memory"))
	if err != nil {
		log.Fatal(err)
	}
	query(db, "SELECT 'alias'")
}
