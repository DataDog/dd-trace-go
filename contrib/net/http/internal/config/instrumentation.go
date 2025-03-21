// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package config

import "github.com/DataDog/dd-trace-go/v2/instrumentation"

var Instrumentation *instrumentation.Instrumentation

func init() {
	Instrumentation = instrumentation.Load(instrumentation.PackageNetHTTP)
}
