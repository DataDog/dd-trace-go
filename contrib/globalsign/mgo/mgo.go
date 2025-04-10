// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package mgo provides functions and types which allow tracing of the MGO MongoDB client (https://github.com/globalsign/mgo)
package mgo // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/globalsign/mgo"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/globalsign/mgo/v2"
)

// Dial opens a connection to a MongoDB server and configures it
// for tracing.
func Dial(url string, opts ...DialOption) (*Session, error) {
	return v2.Dial(url, opts...)
}

// Session is an mgo.Session instance that will be traced.
type Session = v2.Session

// Database is an mgo.Database along with the data necessary for tracing.
type Database = v2.Database

// Iter is an mgo.Iter instance that will be traced.
type Iter = v2.Iter

// Bulk is an mgo.Bulk instance that will be traced.
type Bulk = v2.Bulk
