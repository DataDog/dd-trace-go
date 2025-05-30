// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package mongo provides functions to trace the mongodb/mongo-go-driver package (https://github.com/mongodb/mongo-go-driver).
// It support v0.2.0 of github.com/mongodb/mongo-go-driver
//
// `NewMonitor` will return an event.CommandMonitor which is used to trace requests.
package mongo

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver/v2/mongo"

	"go.mongodb.org/mongo-driver/event"
)

// NewMonitor creates a new mongodb event CommandMonitor.
func NewMonitor(opts ...Option) *event.CommandMonitor {
	return v2.NewMonitor(opts...)
}
