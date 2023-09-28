// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttptrace

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestStartSpanFromContext(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	fctx := &fasthttp.RequestCtx{}
	activeSpan := StartSpanFromContext(fctx, "myOp")
	keySpan := fctx.UserValue(internal.ActiveSpanKey)
	assert.Equal(activeSpan, keySpan)
}
