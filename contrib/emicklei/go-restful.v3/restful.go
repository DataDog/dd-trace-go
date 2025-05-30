// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package restful provides functions to trace the emicklei/go-restful package (https://github.com/emicklei/go-restful).
package restful

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/emicklei/go-restful.v3/v2"

	"github.com/emicklei/go-restful/v3"
)

// FilterFunc returns a restful.FilterFunction which will automatically trace incoming request.
func FilterFunc(configOpts ...Option) restful.FilterFunction {
	return v2.FilterFunc(configOpts...)
}
