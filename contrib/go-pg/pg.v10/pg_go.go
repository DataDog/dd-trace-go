// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package pg

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/go-pg/pg.v10/v2"

	"github.com/go-pg/pg/v10"
)

// Wrap augments the given DB with tracing.
func Wrap(db *pg.DB, opts ...Option) {
	v2.Wrap(db, opts...)
}
