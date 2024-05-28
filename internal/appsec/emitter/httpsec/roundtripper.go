// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"context"
	"net/http"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var badInputContextOnce sync.Once

func RoundTrip(ctx context.Context, request *http.Request, rt http.RoundTripper) (*http.Response, error) {
	url := request.URL.String()
	opArgs := types.RoundTripOperationArgs{
		URL: url,
	}

	parent, _ := ctx.Value(listener.ContextKey{}).(dyngo.Operation)
	if parent == nil { // No parent operation => we can't monitor the request
		badInputContextOnce.Do(func() {
			log.Debug("appsec: outgoing http request monitoring ignored: could not find the http handler " +
				"instrumentation metadata in the request context: the request handler is not being monitored by a " +
				"middleware function or the incoming request context has not be forwarded correctly to the roundtripper")
		})
		return rt.RoundTrip(request)
	}

	op := &types.RoundTripOperation{
		Operation: dyngo.NewOperation(parent),
	}

	var err *events.SecurityBlockingEvent
	dyngo.OnData(op, func(e *events.SecurityBlockingEvent) {
		err = e
	})

	dyngo.StartOperation(op, opArgs)
	dyngo.FinishOperation(op, types.RoundTripOperationRes{})

	if err != nil {
		log.Debug("appsec: outgoing http request blocked by the WAF on URL: %s", url)
		return nil, err
	}

	return rt.RoundTrip(request)
}
