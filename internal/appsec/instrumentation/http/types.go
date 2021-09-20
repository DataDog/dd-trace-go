// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

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
