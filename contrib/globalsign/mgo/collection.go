// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mgo

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/globalsign/mgo/v2"
)

// Collection provides a mgo.Collection along with
// data used for APM Tracing.
type Collection = v2.Collection
