// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

// Package gorm provides helper functions for tracing the jinzhu/gorm package (https://github.com/jinzhu/gorm).
package gorm

import (
	sqltraced "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"

	"github.com/jinzhu/gorm"
)

// Open opens a new (traced) database connection. The used dialect must be formerly registered
// using (gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql).Register.
func Open(dialect, source string) (*gorm.DB, error) {
	db, err := sqltraced.Open(dialect, source)
	if err != nil {
		return nil, err
	}
	return gorm.Open(dialect, db)
}
