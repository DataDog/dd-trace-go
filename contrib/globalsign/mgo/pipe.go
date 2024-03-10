// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mgo

import (
	v2 "github.com/DataDog/dd-trace-go/v2/contrib/globalsign/mgo"
)

// Pipe is an mgo.Pipe instance along with the data necessary for tracing.
type Pipe = v2.Pipe
