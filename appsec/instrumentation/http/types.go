package http

import (
	"net/http"
	"net/url"
)

type (
	HandlerOperationArgs struct {
		Headers   Header
		URL       *url.URL
		UserAgent UserAgent
	}
	HandlerOperationRes struct{}
)

type (
	UserAgent string
	Header    http.Header
)
