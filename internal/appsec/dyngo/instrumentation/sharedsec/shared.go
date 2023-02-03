// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package sharedsec

import (
	"context"
	"reflect"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/grpcsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type (
	// UserIDOperation type representing a call to appsec.SetUser(). It must be created with
	// StartUserIDOperation() and finished with its Finish() method.
	UserIDOperation struct {
		dyngo.Operation
		Block bool
	}
	// UserIDOperationArgs is the user ID operation arguments.
	UserIDOperationArgs struct {
		UserID string
	}
	// UserIDOperationRes is the user ID operation results.
	UserIDOperationRes struct{}

	// OnUserIDOperationStart function type, called when a user ID
	// operation starts.
	OnUserIDOperationStart func(operation *UserIDOperation, args UserIDOperationArgs)
)

var userIDOperationArgsType = reflect.TypeOf((*UserIDOperationArgs)(nil)).Elem()

func StartUserIDOperation(parent dyngo.Operation, args UserIDOperationArgs) *UserIDOperation {
	op := &UserIDOperation{Operation: dyngo.NewOperation(parent)}
	dyngo.StartOperation(op, args)
	return op
}

func (op *UserIDOperation) Finish() {
	dyngo.FinishOperation(op, UserIDOperationRes{})
}

// ListenedType returns the type a OnUserIDOperationStart event listener
// listens to, which is the UserIDOperationStartArgs type.
func (OnUserIDOperationStart) ListenedType() reflect.Type { return userIDOperationArgsType }

// Call the underlying event listener function by performing the type-assertion
// on v whose type is the one returned by ListenedType().
func (f OnUserIDOperationStart) Call(op dyngo.Operation, v interface{}) {
	f(op.(*UserIDOperation), v.(UserIDOperationArgs))
}

// MonitorUser starts and finishes a UserID operation.
// A call to the WAF is made to check the user ID and the returned value
// indicates whether the user should be blocked or not.
func MonitorUser(ctx context.Context, userID string) bool {
	if parent := fromContext(ctx); parent != nil {
		op := StartUserIDOperation(parent, UserIDOperationArgs{UserID: userID})
		op.Finish()
		return op.Block
	}
	log.Error("appsec: user ID monitoring ignored: could not find the http handler instrumentation metadata in the request context: the request handler is not being monitored by a middleware function or the provided context is not the expected request context")
	return false

}

func fromContext(ctx context.Context) dyngo.Operation {
	// Avoid a runtime panic in case of type-assertion error by collecting the 2 return values
	// HTTP context
	if op := httpsec.FromContext(ctx); op != nil {
		return op
	}
	return grpcsec.FromContext(ctx)
}
