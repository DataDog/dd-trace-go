// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcevent_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/datadog/dd-trace-go/dyngo"
	"github.com/datadog/dd-trace-go/dyngo/event/grpcevent"
	"github.com/datadog/dd-trace-go/dyngo/internal/operation"

	"github.com/stretchr/testify/require"
)

type (
	rootArgs struct{}
	rootRes  struct{}
)

func (rootArgs) IsArgOf(operation.Operation)   {}
func (rootRes) IsResultOf(operation.Operation) {}

func TestUsage(t *testing.T) {
	testRPCRepresentation := func(expectedRecvOperation int) func(*testing.T) {
		return func(t *testing.T) {
			localRootOp := operation.NewRoot()
			operation.Start(localRootOp, rootArgs{})
			defer operation.Finish(localRootOp, rootRes{})

			var handlerStarted, handlerFinished, recvStarted, recvFinished int
			defer func() {
				require.Equal(t, 1, handlerStarted)
				require.Equal(t, 1, handlerFinished)
				require.Equal(t, expectedRecvOperation, recvStarted)
				require.Equal(t, expectedRecvOperation, recvFinished)
			}()

			const expectedMessageFormat = "message number %d"
			var secEvents []any

			dyngo.On(localRootOp, func(handlerOp *grpcevent.HandlerOperation, args grpcevent.HandlerOperationArgs) {
				handlerStarted++

				dyngo.On(handlerOp, func(op grpcevent.ReceiveOperation, _ grpcevent.ReceiveOperationArgs) {
					recvStarted++

					dyngo.OnFinish(op, func(_ grpcevent.ReceiveOperation, res grpcevent.ReceiveOperationRes) {
						expectedMessage := fmt.Sprintf(expectedMessageFormat, recvStarted)
						require.Equal(t, expectedMessage, res.Message)
						recvFinished++

						secEvents = append(secEvents, expectedMessage)
					})
				})

				dyngo.OnFinish(handlerOp, func(*grpcevent.HandlerOperation, grpcevent.HandlerOperationRes) { handlerFinished++ })
			})

			_, rpcOp := grpcevent.StartHandlerOperation(context.Background(), localRootOp, grpcevent.HandlerOperationArgs{})

			for i := 1; i <= expectedRecvOperation; i++ {
				recvOp := grpcevent.StartReceiveOperation(rpcOp, grpcevent.ReceiveOperationArgs{})
				recvOp.Finish(grpcevent.ReceiveOperationRes{Message: fmt.Sprintf(expectedMessageFormat, i)})
			}

			rpcOp.Finish(grpcevent.HandlerOperationRes{})

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
