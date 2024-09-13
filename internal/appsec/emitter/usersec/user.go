// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package usersec

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type (
	// UserIDOperation type representing a call to appsec.SetUser(). It gets both created and destroyed in a single
	// call to ExecuteUserIDOperation
	UserIDOperation struct {
		dyngo.Operation
	}
	// UserIDOperationArgs is the user ID operation arguments.
	UserIDOperationArgs struct {
		UserID string
	}
	// UserIDOperationRes is the user ID operation results.
	UserIDOperationRes struct{}
)

// ExecuteUserIDOperation starts and finishes the UserID operation by emitting a dyngo start and finish events
// An error is returned if the user associated to that operation must be blocked
func ExecuteUserIDOperation(parent dyngo.Operation, args UserIDOperationArgs) error {
	var err error
	op := &UserIDOperation{Operation: dyngo.NewOperation(parent)}
	dyngo.OnData(op, func(e *events.BlockingSecurityEvent) { err = e })
	dyngo.StartOperation(op, args)
	dyngo.FinishOperation(op, UserIDOperationRes{})
	return err
}

// MonitorUser starts and finishes a UserID operation.
// A call to the WAF is made to check the user ID and an error is returned if the
// user should be blocked. The return value is nil otherwise.
func MonitorUser(ctx context.Context, userID string) error {
	if parent, ok := dyngo.FromContext(ctx); ok {
		return ExecuteUserIDOperation(parent, UserIDOperationArgs{UserID: userID})
	}
	log.Error("appsec: user ID monitoring ignored: could not find the http handler instrumentation metadata in the request context: the request handler is not being monitored by a middleware function or the provided context is not the expected request context")
	return nil

}

func (UserIDOperationArgs) IsArgOf(*UserIDOperation)   {}
func (UserIDOperationRes) IsResultOf(*UserIDOperation) {}
