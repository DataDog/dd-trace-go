// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package businessevent

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/opcontext"
	"gopkg.in/DataDog/dd-trace-go.v1/dyngo/internal/operation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type (
	// UserAuthNOperation represents a user authentication operation.
	UserAuthNOperation struct {
		operation.Operation
	}

	// UserAuthNOperationArgs is the user authentication operation's arguments.
	UserAuthNOperationArgs struct {
		UserID string `address:"usr.id"` // The use ID which has been authenticated
	}

	// UserAuthNOperationRes is the user authentication operation's results.
	UserAuthNOperationRes struct{}
)

// FireUserAuthenticationOperation starts and finishes a user authentication
// operation within the supplied parent operation, using the provided argumnets.
// It returns an error if the operation must be blocked.
func FireUserAuthenticationOperation(
	parent operation.Operation,
	args UserAuthNOperationArgs,
) (err error) {
	op := &UserAuthNOperation{Operation: operation.New(parent)}
	operation.OnData(op, func(data error) { err = data })
	operation.Start(op, args)
	operation.Finish(op, UserAuthNOperationRes{})
	return
}

// MonitorUserAuthentication propagates a user authentication operation in the
// operations stack from the provided context, using the supplied user ID. If
// operation listeners determine the authentication should be blocked, an error
// is returned which can be used to determine how the blocking should be done.
func MonitorUserAuthentication(ctx context.Context, userID string) error {
	op := opcontext.Operation(ctx)
	if op == nil {
		log.Error("dyngo: user ID monitoring ignored, no Operation was found in context. Either the current request is not being monitored, or the provided context is not correct.")
		return nil
	}
	return FireUserAuthenticationOperation(op, UserAuthNOperationArgs{UserID: userID})
}

func (UserAuthNOperationArgs) IsArgOf(*UserAuthNOperation)   {}
func (UserAuthNOperationRes) IsResultOf(*UserAuthNOperation) {}
