// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcsec_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/grpcsec"
)

func TestUsage(t *testing.T) {
	testRPCRepresentation := func(expectedRecvOperation int) func(*testing.T) {
		return func(t *testing.T) {
			type (
				rootArgs struct{}
				rootRes  struct{}
			)
			localRootOp := dyngo.NewOperation(nil)
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

			localRootOp.On(grpcsec.OnHandlerOperationStart(func(handlerOp *grpcsec.HandlerOperation, args grpcsec.HandlerOperationArgs) {
				handlerStarted++

				handlerOp.On(grpcsec.OnReceiveOperationStart(func(op grpcsec.ReceiveOperation, _ grpcsec.ReceiveOperationArgs) {
					recvStarted++

					op.On(grpcsec.OnReceiveOperationFinish(func(_ grpcsec.ReceiveOperation, res grpcsec.ReceiveOperationRes) {
						expectedMessage := fmt.Sprintf(expectedMessageFormat, recvStarted)
						require.Equal(t, expectedMessage, res.Message)
						recvFinished++

						handlerOp.AddSecurityEvent([]json.RawMessage{json.RawMessage(expectedMessage)})
					}))
				}))

				handlerOp.On(grpcsec.OnHandlerOperationFinish(func(*grpcsec.HandlerOperation, grpcsec.HandlerOperationRes) {
					handlerFinished++
				}))
			}))

			rpcOp := grpcsec.StartHandlerOperation(grpcsec.HandlerOperationArgs{}, localRootOp)

			for i := 1; i <= expectedRecvOperation; i++ {
				recvOp := grpcsec.StartReceiveOperation(grpcsec.ReceiveOperationArgs{}, rpcOp)
				recvOp.Finish(grpcsec.ReceiveOperationRes{Message: fmt.Sprintf(expectedMessageFormat, i)})
			}

			secEvents := rpcOp.Finish(grpcsec.HandlerOperationRes{})

			require.Len(t, secEvents, expectedRecvOperation)
			for i, e := range secEvents {
				require.Equal(t, fmt.Sprintf(expectedMessageFormat, i+1), string(e))
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
