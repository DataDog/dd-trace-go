// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package gin provides functions to trace the gin-gonic/gin package (https://github.com/gin-gonic/gin).
package gin // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gin-gonic/gin"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin/v2"

	"github.com/gin-gonic/gin"
)

// Middleware returns middleware that will trace incoming requests. If service is empty then the
// default service name will be used.
func Middleware(service string, opts ...Option) gin.HandlerFunc {
	return v2.Middleware(service, opts...)
}

// HTML will trace the rendering of the template as a child of the span in the given context.
func HTML(c *gin.Context, code int, name string, obj interface{}) {
	v2.HTML(c, code, name, obj)
}
