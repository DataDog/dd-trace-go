// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcsec_test

import (
	"context"
	"fmt"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	grpcsec "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/grpcsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/grpcsec/types"

	"github.com/stretchr/testify/require"
)

type (
	rootArgs struct{}
	rootRes  struct{}
)

func (rootArgs) IsArgOf(dyngo.Operation)   {}
func (rootRes) IsResultOf(dyngo.Operation) {}

func TestUsage(t *testing.T) {
	testRPCRepresentation := func(expectedRecvOperation int) func(*testing.T) {
		return func(t *testing.T) {
			localRootOp := dyngo.NewRootOperation()
			dyngo.StartOperation(localRootOp, rootArgs{})
			defer dyngo.FinishOperation(localRootOp, rootRes{})

			var handlerStarted, handlerFinished, recvStarted, recvFinished int
			defer func() {
				require.Equal(t, 1, handlerStarted)
				require.Equal(t, 1, handlerFinished)
				require.Equal(t, expectedRecvOperation, recvStarted)
				require.Equal(t, expectedRecvOperation, recvFinished)
			}()

			const expectedMessageFormat = "message number %d"

			dyngo.On(localRootOp, func(handlerOp *types.HandlerOperation, args types.HandlerOperationArgs) {
				handlerStarted++

				dyngo.On(handlerOp, func(op types.ReceiveOperation, _ types.ReceiveOperationArgs) {
					recvStarted++

					dyngo.OnFinish(op, func(_ types.ReceiveOperation, res types.ReceiveOperationRes) {
						expectedMessage := fmt.Sprintf(expectedMessageFormat, recvStarted)
						require.Equal(t, expectedMessage, res.Message)
						recvFinished++

						handlerOp.AddSecurityEvents([]any{expectedMessage})
					})
				})

				dyngo.OnFinish(handlerOp, func(*types.HandlerOperation, types.HandlerOperationRes) { handlerFinished++ })
			})

			_, rpcOp := grpcsec.StartHandlerOperation(context.Background(), types.HandlerOperationArgs{}, localRootOp)

			for i := 1; i <= expectedRecvOperation; i++ {
				recvOp := grpcsec.StartReceiveOperation(types.ReceiveOperationArgs{}, rpcOp)
				recvOp.Finish(types.ReceiveOperationRes{Message: fmt.Sprintf(expectedMessageFormat, i)})
			}

			secEvents := rpcOp.Finish(types.HandlerOperationRes{})

			require.Len(t, secEvents, expectedRecvOperation)
			for i, e := range secEvents {
				require.Equal(t, fmt.Sprintf(expectedMessageFormat, i+1), e)
			}
		}
	}

	// Unary RPCs are represented by a single receive operation
	t.Run("unary-representation", testRPCRepresentation(1))
	// Client streaming RPCs are represented by many receive operations.
	t.Run("client-streaming-representation", testRPCRepresentation(10))
	// Server and bidirectional streaming RPCs cannot be tested for now because
	// the send operations are not used nor defined yet, server streaming RPCs
	// are currently represented like unary RPCs (1 client message, N server
	// messages), and bidirectional RPCs like client streaming RPCs (N client
	// messages, M server messages).
}
