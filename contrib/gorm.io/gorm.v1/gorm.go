// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package gorm provides helper functions for tracing the gorm.io/gorm package (https://github.com/go-gorm/gorm).
package gorm

import (
	v2 "github.com/DataDog/dd-trace-go/v2/contrib/gorm.io/gorm.v1"

	"gorm.io/gorm"
)

// Open opens a new (traced) database connection. The used driver must be formerly registered
// using (gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql).Register.
func Open(dialector gorm.Dialector, cfg *gorm.Config, opts ...Option) (*gorm.DB, error) {
	return v2.Open(dialector, cfg, opts...)
}
