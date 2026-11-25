// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import (
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var Instrumentation *instrumentation.Instrumentation

// initialized is set once Instrumentation is.
var initialized atomic.Bool

func init() {
	Instrumentation = instrumentation.Load(instrumentation.PackageNetHTTP)
	initialized.Store(true)
}

// Initialized reports whether this package is initialized. Orchestrion's
// net/http wrappers can run before it is: a server or a request that another
// package's init function starts runs concurrently with the initialization of
// the packages that come after it.
func Initialized() bool {
	return initialized.Load()
}
