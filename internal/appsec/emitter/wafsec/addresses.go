// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package wafsec

import (
	waf "github.com/DataDog/go-libddwaf/v3"
)

const (
	ServerRequestMethodAddr            = "server.request.method"
	ServerRequestRawURIAddr            = "server.request.uri.raw"
	ServerRequestHeadersNoCookiesAddr  = "server.request.headers.no_cookies"
	ServerRequestCookiesAddr           = "server.request.cookies"
	ServerRequestQueryAddr             = "server.request.query"
	ServerRequestPathParamsAddr        = "server.request.path_params"
	ServerRequestBodyAddr              = "server.request.body"
	ServerResponseStatusAddr           = "server.response.status"
	ServerResponseHeadersNoCookiesAddr = "server.response.headers.no_cookies"

	HTTPClientIPAddr = "http.client_ip"
	UserIDAddr       = "usr.id"

	ServerIoNetURLAddr    = "server.io.net.url"
	ServerDBStatementAddr = "server.db.statement"
	ServerDBTypeAddr      = "server.db.system"

	GRPCServerMethodAddr          = "grpc.server.method"
	GRPCServerRequestMessageAddr  = "grpc.server.request.message"
	GRPCServerRequestMetadataAddr = "grpc.server.request.metadata"

	GraphQLServerResolverAddr = "graphql.server.resolver"
)

// AddressSet is a set of WAF addresses.
type AddressSet map[string]struct{}

// FilterAddressSet filters the supplied `supported` address set to only include
// entries referenced by the supplied waf.Handle.
func FilterAddressSet(supported AddressSet, handle *waf.Handle) AddressSet {
	result := make(AddressSet, len(supported))

	for _, addr := range handle.Addresses() {
		if _, found := supported[addr]; found {
			result[addr] = struct{}{}
		}
	}

	return result
}
