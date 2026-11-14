// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gorm

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// failingDialector is a dialector whose Initialize fails.
type failingDialector struct {
	gorm.Dialector
}

func (failingDialector) Initialize(*gorm.DB) error {
	return errors.New("initialize failed")
}

// TestOpenError checks that an instrumented gorm.Open that fails returns the
// *gorm.DB with the error, like the uninstrumented call.
func TestOpenError(t *testing.T) {
	db, err := gorm.Open(failingDialector{sqlite.Open("file::memory:")}, &gorm.Config{})
	require.Error(t, err)
	require.NotNil(t, db)
}
