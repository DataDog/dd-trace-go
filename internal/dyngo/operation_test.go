package dyngo_test

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"

	"github.com/stretchr/testify/require"
)

// Dummy struct to mimic real-life operation stacks.
type (
	HTTPHandlerArgs struct {
		URL     *url.URL
		Headers http.Header
	}
	HTTPHandlerResult struct{}
	SQLQueryArg       struct {
		Query string
	}
	SQLQueryResult struct {
		Err error
	}
	GRPCHandlerArg struct {
		Msg interface{}
	}
	GRPCHandlerResult struct {
		Res interface{}
	}
	JSONParserArg struct {
		Buf []byte
	}
	JSONParserResults struct {
		Value interface{}
		Err   error
	}
	BodyReadArg struct{}
	BodyReadRes struct {
		Buf []byte
		Err error
	}
)

func TestUsage(t *testing.T) {
	t.Run("Operation stacking", func(t *testing.T) {
		// Dummy waf looking for the string `attack` in HTTPHandlerArgs
		wafListener := func(called *int, blocked *bool) dyngo.EventListener {
			return dyngo.OnStartEventListener(func(_ *dyngo.Operation, args HTTPHandlerArgs) {
				*called++

				if strings.Contains(args.URL.RawQuery, "attack") {
					*blocked = true
					return
				}
				for _, values := range args.Headers {
					for _, v := range values {
						if strings.Contains(v, "attack") {
							*blocked = true
							return
						}
					}
				}
			})
		}

		// HTTP body read listener appending the read results to a buffer
		rawBodyListener := func(called *int, buf *[]byte) dyngo.EventListener {
			return dyngo.OnStartEventListener(func(op *dyngo.Operation, args HTTPHandlerArgs) {

				op.OnFinish(func(_ *dyngo.Operation, res BodyReadRes) {
					*called++
					*buf = append(*buf, res.Buf...)
					if res.Err == io.EOF {
						// TODO: emit raw body data
					}
				})
			})
		}

		jsonBodyValueListener := func(called *int, value *interface{}) dyngo.EventListener {
			return dyngo.OnStartEventListener(func(op *dyngo.Operation, args HTTPHandlerArgs) {
				didBodyRead := false
				op.OnFinish(func(op *dyngo.Operation, res JSONParserResults) {
					*called++
					if !didBodyRead || res.Err != nil {
						return
					}
					*value = res.Value
				})
				op.OnStart(func(_ *dyngo.Operation, res BodyReadArg) {
					didBodyRead = true
				})
			})
		}

		t.Run("stack monitored and not blocked by waf", func(t *testing.T) {
			root := dyngo.StartOperation(struct{}{})

			var (
				WAFBlocked bool
				WAFCalled  int
			)
			wafListener := wafListener(&WAFCalled, &WAFBlocked)

			var (
				RawBodyBuf    []byte
				RawBodyCalled int
			)
			rawBodyListener := rawBodyListener(&RawBodyCalled, &RawBodyBuf)

			var (
				JSONBodyParserValue  interface{}
				JSONBodyParserCalled int
			)
			jsonBodyValueListener := jsonBodyValueListener(&JSONBodyParserCalled, &JSONBodyParserValue)

			root.Register(rawBodyListener, wafListener, jsonBodyValueListener)

			// Run the monitored stack of operations
			operation(
				root,
				HTTPHandlerArgs{
					URL:     &url.URL{RawQuery: "?v=ok"},
					Headers: http.Header{"header": []string{"value"}}},
				HTTPHandlerResult{},
				func(op *dyngo.Operation) {
					operation(op, JSONParserArg{}, JSONParserResults{Value: []interface{}{"a", "json", "array"}}, func(op *dyngo.Operation) {
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("my ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("bo")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("dy"), Err: io.EOF}, nil)
					})
					operation(op, SQLQueryArg{}, SQLQueryArg{}, nil)
				},
			)

			// WAF callback called without blocking
			require.False(t, WAFBlocked)
			require.Equal(t, 1, WAFCalled)

			// The raw body listener has been called
			require.Equal(t, []byte("my raw body"), RawBodyBuf)
			require.Equal(t, 4, RawBodyCalled)

			// The json body value listener has been called
			require.Equal(t, 1, JSONBodyParserCalled)
			require.Equal(t, []interface{}{"a", "json", "array"}, JSONBodyParserValue)
		})

		t.Run("stack monitored and blocked by waf", func(t *testing.T) {
			root := dyngo.StartOperation(struct{}{})

			var (
				WAFBlocked bool
				WAFCalled  int
			)
			wafListener := wafListener(&WAFCalled, &WAFBlocked)

			var (
				RawBodyBuf    []byte
				RawBodyCalled int
			)
			rawBodyListener := rawBodyListener(&RawBodyCalled, &RawBodyBuf)

			var (
				JSONBodyParserValue  interface{}
				JSONBodyParserCalled int
			)
			jsonBodyValueListener := jsonBodyValueListener(&JSONBodyParserCalled, &JSONBodyParserValue)

			root.Register(rawBodyListener, wafListener, jsonBodyValueListener)

			// Run the monitored stack of operations
			RawBodyBuf = nil
			operation(
				root,
				HTTPHandlerArgs{
					URL:     &url.URL{RawQuery: "?v=attack"},
					Headers: http.Header{"header": []string{"value"}}},
				HTTPHandlerResult{},
				func(op *dyngo.Operation) {
					operation(op, JSONParserArg{}, JSONParserResults{Value: "a string", Err: errors.New("an error")}, func(op *dyngo.Operation) {
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("another ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("bo")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("dy"), Err: nil}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte(" value"), Err: io.EOF}, nil)
					})

					operation(op, SQLQueryArg{}, SQLQueryArg{}, nil)
				},
			)

			// WAF callback called and blocked
			require.True(t, WAFBlocked)
			require.Equal(t, 1, WAFCalled)

			// The raw body listener has been called
			require.Equal(t, 5, RawBodyCalled)
			require.Equal(t, []byte("another raw body value"), RawBodyBuf)

			// The json body value listener has been called but no value due to a parser error
			require.Equal(t, 1, JSONBodyParserCalled)
			require.Equal(t, nil, JSONBodyParserValue)
		})

		t.Run("stack not monitored", func(t *testing.T) {
			root := dyngo.StartOperation(struct{}{})

			var (
				WAFBlocked bool
				WAFCalled  int
			)
			wafListener := wafListener(&WAFCalled, &WAFBlocked)

			var (
				RawBodyBuf    []byte
				RawBodyCalled int
			)
			rawBodyListener := rawBodyListener(&RawBodyCalled, &RawBodyBuf)

			var (
				JSONBodyParserValue  interface{}
				JSONBodyParserCalled int
			)
			jsonBodyValueListener := jsonBodyValueListener(&JSONBodyParserCalled, &JSONBodyParserValue)

			root.Register(rawBodyListener, wafListener, jsonBodyValueListener)

			// Run the monitored stack of operations
			operation(
				root,
				GRPCHandlerArg{}, GRPCHandlerResult{},
				func(op *dyngo.Operation) {
					operation(op, JSONParserArg{}, JSONParserResults{Value: []interface{}{"a", "json", "array"}}, func(op *dyngo.Operation) {
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("my ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("bo")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("dy"), Err: io.EOF}, nil)
					})
					operation(op, SQLQueryArg{}, SQLQueryResult{}, nil)
				},
			)

			// WAF callback called without blocking
			require.False(t, WAFBlocked)
			require.Equal(t, 0, WAFCalled)

			// The raw body listener has been called
			require.Nil(t, RawBodyBuf)
			require.Equal(t, 0, RawBodyCalled)

			// The json body value listener has been called
			require.Equal(t, 0, JSONBodyParserCalled)
			require.Nil(t, JSONBodyParserValue)
		})
	})
}

type (
	MyOperationArgs struct{}
	MyOperationData struct{}
	MyOperationRes  struct{}
)

func TestRegisterUnregister(t *testing.T) {
	var onStartCalled, onDataCalled, onFinishCalled int

	ids := dyngo.Register(
		dyngo.InstrumentationDescriptor{
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: dyngo.OnStartEventListener(func(*dyngo.Operation, MyOperationArgs) {
					onStartCalled++
				}),
			},
		},
		dyngo.InstrumentationDescriptor{
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: dyngo.OnStartEventListener(func(*dyngo.Operation, MyOperationArgs) {
					onDataCalled++
				}),
			},
		},
		dyngo.InstrumentationDescriptor{
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: dyngo.OnFinishEventListener(func(*dyngo.Operation, MyOperationRes) {
					onFinishCalled++
				}),
			},
		},
	)

	operation(nil, MyOperationArgs{}, MyOperationRes{}, func(op *dyngo.Operation) {
		op.EmitData(MyOperationData{})
	})
	require.Equal(t, 1, onStartCalled)
	require.Equal(t, 1, onDataCalled)
	require.Equal(t, 1, onFinishCalled)

	dyngo.Unregister(ids)
	operation(nil, MyOperationArgs{}, MyOperationRes{}, func(op *dyngo.Operation) {
		op.EmitData(MyOperationData{})
	})
	require.Equal(t, 1, onStartCalled)
	require.Equal(t, 1, onDataCalled)
	require.Equal(t, 1, onFinishCalled)

}

func operation(parent *dyngo.Operation, args, res interface{}, child func(*dyngo.Operation)) {
	op := dyngo.StartOperation(args, dyngo.WithParent(parent))
	defer op.Finish(res)
	if child != nil {
		child(op)
	}
}
