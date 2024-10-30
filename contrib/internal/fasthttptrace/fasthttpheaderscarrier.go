// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttptrace

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/valyala/fasthttp"
)

// HTTPHeadersCarrier implements tracer.TextMapWriter and tracer.TextMapReader on top
// of fasthttp's RequestHeader object, allowing it to be used as a span context carrier for
// distributed tracing.
type HTTPHeadersCarrier struct {
	ReqHeader *fasthttp.RequestHeader
}

var _ tracer.TextMapWriter = (*HTTPHeadersCarrier)(nil)
var _ tracer.TextMapReader = (*HTTPHeadersCarrier)(nil)

// ForeachKey iterates over fasthttp request header keys and values
func (f *HTTPHeadersCarrier) ForeachKey(handler func(key, val string) error) error {
	keys := f.ReqHeader.PeekKeys()
	for _, key := range keys {
		sKey := string(key)
		v := f.ReqHeader.Peek(sKey)
		if err := handler(sKey, string(v)); err != nil {
			return err
		}
	}
	return nil
}

// Set adds the given value to request header for key. Key will be lowercased to match
// the metadata implementation.
func (f *HTTPHeadersCarrier) Set(key, val string) {
	f.ReqHeader.Set(key, val)
}
