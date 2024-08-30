// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package wafsec

import (
	"net/netip"

	waf "github.com/DataDog/go-libddwaf/v3"
)

type RunAddressDataBuilder struct {
	waf.RunAddressData
}

func NewRunAddressDataBuilder() *RunAddressDataBuilder {
	return &RunAddressDataBuilder{
		RunAddressData: waf.RunAddressData{
			Persistent: map[string]any{},
			Ephemeral:  map[string]any{},
		},
	}
}

func (b *RunAddressDataBuilder) WithMethod(method string) *RunAddressDataBuilder {
	if method == "" {
		return b
	}
	b.Persistent[ServerRequestMethodAddr] = method
	return b
}

func (b *RunAddressDataBuilder) WithRawURI(uri string) *RunAddressDataBuilder {
	if uri == "" {
		return b
	}
	b.Persistent[ServerRequestRawURIAddr] = uri
	return b
}

func (b *RunAddressDataBuilder) WithHeadersNoCookies(headers map[string][]string) *RunAddressDataBuilder {
	if len(headers) == 0 {
		return b
	}
	b.Persistent[ServerRequestHeadersNoCookiesAddr] = headers
	return b
}

func (b *RunAddressDataBuilder) WithCookies(cookies map[string][]string) *RunAddressDataBuilder {
	if len(cookies) == 0 {
		return b
	}
	b.Persistent[ServerRequestCookiesAddr] = cookies
	return b
}

func (b *RunAddressDataBuilder) WithQuery(query map[string][]string) *RunAddressDataBuilder {
	if len(query) == 0 {
		return b
	}
	b.Persistent[ServerRequestQueryAddr] = query
	return b
}

func (b *RunAddressDataBuilder) WithPathParams(params map[string]string) *RunAddressDataBuilder {
	if len(params) == 0 {
		return b
	}
	b.Persistent[ServerRequestPathParamsAddr] = params
	return b
}

func (b *RunAddressDataBuilder) WithBody(body any) *RunAddressDataBuilder {
	if body == nil {
		return b
	}
	b.Persistent[ServerRequestBodyAddr] = body
	return b
}

func (b *RunAddressDataBuilder) WithResponseStatus(status int) *RunAddressDataBuilder {
	if status == 0 {
		return b
	}
	b.Persistent[ServerResponseStatusAddr] = status
	return b
}

func (b *RunAddressDataBuilder) WithResponseHeadersNoCookies(headers map[string][]string) *RunAddressDataBuilder {
	if len(headers) == 0 {
		return b
	}
	b.Persistent[ServerResponseHeadersNoCookiesAddr] = headers
	return b
}

func (b *RunAddressDataBuilder) WithClientIP(ip netip.Addr) *RunAddressDataBuilder {
	if !ip.IsValid() {
		return b
	}
	b.Persistent[HTTPClientIPAddr] = ip.String()
	return b
}

func (b *RunAddressDataBuilder) WithUserID(id string) *RunAddressDataBuilder {
	if id == "" {
		return b
	}
	b.Persistent[UserIDAddr] = id
	return b
}

func (b *RunAddressDataBuilder) WithIoNetURL(url string) *RunAddressDataBuilder {
	if url == "" {
		return b
	}
	b.Ephemeral[ServerIoNetURLAddr] = url
	return b
}

func (b *RunAddressDataBuilder) WithDBStatement(statement string) *RunAddressDataBuilder {
	if statement == "" {
		return b
	}
	b.Ephemeral[ServerDBStatementAddr] = statement
	return b
}

func (b *RunAddressDataBuilder) WithDBType(driver string) *RunAddressDataBuilder {
	if driver == "" {
		return b
	}
	b.Ephemeral[ServerDBTypeAddr] = driver
	return b
}

func (b *RunAddressDataBuilder) WithGRPCMethod(method string) *RunAddressDataBuilder {
	if method == "" {
		return b
	}
	b.Persistent[GRPCServerMethodAddr] = method
	return b
}

func (b *RunAddressDataBuilder) WithGRPCRequestMessage(message any) *RunAddressDataBuilder {
	if message == nil {
		return b
	}
	b.Ephemeral[GRPCServerRequestMessageAddr] = message
	return b
}

func (b *RunAddressDataBuilder) WithGRPCRequestMetadata(metadata map[string][]string) *RunAddressDataBuilder {
	if len(metadata) == 0 {
		return b
	}
	b.Persistent[GRPCServerRequestMetadataAddr] = metadata
	return b
}

func (b *RunAddressDataBuilder) WithGraphQLResolver(resolver string) *RunAddressDataBuilder {
	if resolver == "" {
		return b
	}
	b.Ephemeral[GraphQLServerResolverAddr] = resolver
	return b
}

func (b *RunAddressDataBuilder) ExtractSchema() *RunAddressDataBuilder {
	b.Persistent["waf.context.processor"] = map[string]bool{"extract-schema": true}
	return b
}

func (b *RunAddressDataBuilder) Build() waf.RunAddressData {
	return b.RunAddressData
}
