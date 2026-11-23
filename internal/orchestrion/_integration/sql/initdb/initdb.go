// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package initdb opens a database from a package-level variable. It imports
// nothing from dd-trace-go, so Go can initialize it before the tracer.
package initdb

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

var DB, OpenErr = sql.Open("sqlite3", "file::memory:")
