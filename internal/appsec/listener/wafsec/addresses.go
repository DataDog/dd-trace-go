// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package wafsec

type WAFAddress string

const (
	ServerRequestMethodAddr            WAFAddress = "server.request.method"
	ServerRequestRawURIAddr            WAFAddress = "server.request.uri.raw"
	ServerRequestHeadersNoCookiesAddr  WAFAddress = "server.request.headers.no_cookies"
	ServerRequestCookiesAddr           WAFAddress = "server.request.cookies"
	ServerRequestQueryAddr             WAFAddress = "server.request.query"
	ServerRequestPathParamsAddr        WAFAddress = "server.request.path_params"
	ServerRequestBodyAddr              WAFAddress = "server.request.body"
	ServerResponseStatusAddr           WAFAddress = "server.response.status"
	ServerResponseHeadersNoCookiesAddr WAFAddress = "server.response.headers.no_cookies"
	HTTPClientIPAddr                   WAFAddress = "http.client_ip"
	UserIDAddr                         WAFAddress = "usr.id"
	ServerIoNetURLAddr                 WAFAddress = "server.io.net.url"
)
