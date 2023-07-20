// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gearboxutil // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gogearbox/gearbox/internal/gearboxutil"

// MTOFF: This will be useful if we add a fasthttp integration. Might also be useful for the fiber integration.
// But should it be separated under a fashttputils....

import (
	"strings"

	"github.com/valyala/fasthttp"
)

// FasthttpCarrier implements tracer.TextMapWriter and tracer.TextMapReader on top
// of fasthttp's RequestHeader object, allowing it to be used as a span context carrier for
// distributed tracing.
// type FasthttpCarrier struct {
// 	reqHeader *fasthttp.RequestHeader
// }

type FasthttpCarrier struct {
	ReqHeader *fasthttp.RequestHeader 
}

// ForeachKey iterates over fasthttp request header keys and values
func (f *FasthttpCarrier) ForeachKey(handler func(key, val string) error) error {
	keys := f.ReqHeader.PeekKeys()
	for _, key := range keys {
		sKey := string(key)
		vs := f.ReqHeader.PeekAll(sKey)
		for _, v := range vs {
			if err := handler(sKey, string(v)); err != nil {
				return err
			}
		}
	}
	return nil
}
// func (f *FasthttpCarrier) ForeachKey(handler func(key, val string) error) (err error) {
// 	f.ReqHeader.VisitAll(func(k, v []byte) {
// 		if e := handler(string(k), string(v)); e != nil {
// 			err = e
// 			// Break here to quick return
// 		}
// 	})
// 	return err
// }

// Set adds the given value to request header for key. Key will be lowercased to match
// the metadata implementation.
func (f *FasthttpCarrier) Set(key, val string) {
	k := strings.ToLower(key)
	// f.ReqHeader.Set(k, val)
	// MOTFF: "Set" overwrites any value at `k`. "Add" appends it. Just confirming we want to append, not overwrite
	f.ReqHeader.Add(k, val)
}

// Get will return the first entry in the metadata at the given key.
func (f *FasthttpCarrier) Get(key string) string {
	return string(f.ReqHeader.Peek(key))
}