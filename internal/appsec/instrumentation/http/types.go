package http

import (
	"net/http"
	"net/url"
)

type (
	HandlerOperationArgs struct {
		Method      Method
		Host        Host
		RequestURI  RequestURI
		RemoteAddr  RemoteAddr
		Headers     Header
		QueryValues QueryValues
		UserAgent   UserAgent
		IsTLS       bool
	}

	HandlerOperationRes struct {
		Status int
	}

	Method      string
	Host        string
	RequestURI  string
	RemoteAddr  string
	UserAgent   string
	Header      http.Header
	QueryValues url.Values
)
