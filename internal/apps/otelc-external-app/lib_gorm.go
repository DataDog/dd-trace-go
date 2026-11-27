// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"gorm.io/gorm"
	gormtests "gorm.io/gorm/utils/tests"
)

func init() {
	registerLibrary("gorm", func() {
		if _, err := gorm.Open(gormtests.DummyDialector{}); err != nil {
			panic(err)
		}
	})
}
