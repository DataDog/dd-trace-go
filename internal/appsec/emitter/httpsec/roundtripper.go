// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"context"
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type RoundTripArgs struct {
	Ctx context.Context
	Req *http.Request
	Rt  http.RoundTripper
}

func RoundTrip(args RoundTripArgs) (*http.Response, error) {
	url := args.Req.URL.String()
	opArgs := types.RoundTripOperationArgs{
		URL: url,
	}

	parent := fromContext(args.Ctx)
	if parent == nil { // No parent operation => we can't monitor the request
		log.Error("appsec: outgoing http request monitoring ignored: could not find the http handler instrumentation metadata in the request context: the request handler is not being monitored by a middleware function or the provided context has not be forwarded correctly")
		return args.Rt.RoundTrip(args.Req)
	}

	op := &types.RoundTripOperation{Operation: dyngo.NewOperation(parent)}

	var err error

	// Listen for errors in case the request gets blocked
	dyngo.OnData(op, func(e error) {
		err = e
	})
	dyngo.StartOperation(op, opArgs)
	dyngo.FinishOperation(op, types.RoundTripOperationRes{})

	if err != nil {
		log.Error("appsec: outgoing http request blocked by the WAF on URL: %s", url)
		return nil, err
	}

	return args.Rt.RoundTrip(args.Req)
}
