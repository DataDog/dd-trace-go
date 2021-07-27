package http

import (
	"gopkg.in/DataDog/dd-trace-go.v1/appsec/dyngo"
	"net/http"
	"net/url"
)

type (
	HandlerOperationArgs struct {
		Headers     Header
		QueryValues QueryValues
		UserAgent   UserAgent
	}
	HandlerOperationRes struct{}
)

type (
	UserAgent   string
	Header      http.Header
	QueryValues url.Values
)

func EmitData(op *dyngo.Operation, args HandlerOperationArgs) {
	if len(args.Headers) > 0 {
		op.EmitData(args.Headers)
	}
	op.EmitData(args.UserAgent)
	if len(args.QueryValues) > 0 {
		op.EmitData(args.QueryValues)
	}
}
