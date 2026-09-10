// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package addresses

import (
	"net/netip"
	"strconv"

	libddwaf "github.com/DataDog/go-libddwaf/v5"
)

const contextProcessKey = "waf.context.processor"

type RunAddressData = libddwaf.RunAddressData

type RunAddressDataBuilder struct {
	data RunAddressData
}

func NewAddressesBuilder() *RunAddressDataBuilder {
	return &RunAddressDataBuilder{
		data: RunAddressData{
			Data:     make(map[string]any, 1),
			TimerKey: WAFScope,
		},
	}
}

func (b *RunAddressDataBuilder) setPersistent(address string, value any) {
	b.data.Data[address] = value
}

func (b *RunAddressDataBuilder) setEphemeral(address string, value any) {
	b.data.Data[address] = value
}

func (b *RunAddressDataBuilder) setRASPEphemeral(address string, value any) {
	b.setEphemeral(address, value)
	b.data.TimerKey = RASPScope
}

func (b *RunAddressDataBuilder) WithMethod(method string) *RunAddressDataBuilder {
	b.setPersistent(ServerRequestMethodAddr, method)
	return b
}

func (b *RunAddressDataBuilder) WithRawURI(uri string) *RunAddressDataBuilder {
	b.setPersistent(ServerRequestRawURIAddr, uri)
	return b
}

func (b *RunAddressDataBuilder) WithHeadersNoCookies(headers map[string][]string) *RunAddressDataBuilder {
	if len(headers) == 0 {
		headers = nil
	}
	b.setPersistent(ServerRequestHeadersNoCookiesAddr, headers)
	return b
}

func (b *RunAddressDataBuilder) WithCookies(cookies map[string][]string) *RunAddressDataBuilder {
	if len(cookies) == 0 {
		return b
	}
	b.setPersistent(ServerRequestCookiesAddr, cookies)
	return b
}

func (b *RunAddressDataBuilder) WithQuery(query map[string][]string) *RunAddressDataBuilder {
	if len(query) == 0 {
		query = nil
	}
	b.setPersistent(ServerRequestQueryAddr, query)
	return b
}

func (b *RunAddressDataBuilder) WithPathParams(params map[string]string) *RunAddressDataBuilder {
	if len(params) == 0 {
		return b
	}
	b.setPersistent(ServerRequestPathParamsAddr, params)
	return b
}

func (b *RunAddressDataBuilder) WithRequestBody(body any) *RunAddressDataBuilder {
	if body == nil {
		return b
	}
	b.setPersistent(ServerRequestBodyAddr, body)
	return b
}

func (b *RunAddressDataBuilder) WithResponseBody(body any) *RunAddressDataBuilder {
	if body == nil {
		return b
	}
	b.setPersistent(ServerResponseBodyAddr, body)
	return b
}

func (b *RunAddressDataBuilder) WithResponseStatus(status int) *RunAddressDataBuilder {
	if status == 0 {
		return b
	}
	b.setPersistent(ServerResponseStatusAddr, strconv.Itoa(status))
	return b
}

func (b *RunAddressDataBuilder) WithResponseHeadersNoCookies(headers map[string][]string) *RunAddressDataBuilder {
	if len(headers) == 0 {
		return b
	}
	b.setPersistent(ServerResponseHeadersNoCookiesAddr, headers)
	return b
}

func (b *RunAddressDataBuilder) WithClientIP(ip netip.Addr) *RunAddressDataBuilder {
	if !ip.IsValid() {
		return b
	}
	b.setPersistent(ClientIPAddr, ip.String())
	return b
}

func (b *RunAddressDataBuilder) WithUserID(id string) *RunAddressDataBuilder {
	if id == "" {
		return b
	}
	b.setPersistent(UserIDAddr, id)
	return b
}

func (b *RunAddressDataBuilder) WithUserLogin(login string) *RunAddressDataBuilder {
	if login == "" {
		return b
	}
	b.setPersistent(UserLoginAddr, login)
	return b
}

func (b *RunAddressDataBuilder) WithUserOrg(org string) *RunAddressDataBuilder {
	if org == "" {
		return b
	}
	b.setPersistent(UserOrgAddr, org)
	return b
}

func (b *RunAddressDataBuilder) WithUserSessionID(id string) *RunAddressDataBuilder {
	if id == "" {
		return b
	}
	b.setPersistent(UserSessionIDAddr, id)
	return b

}

func (b *RunAddressDataBuilder) WithUserLoginSuccess() *RunAddressDataBuilder {
	b.setPersistent(UserLoginSuccessAddr, nil)
	return b
}

func (b *RunAddressDataBuilder) WithUserLoginFailure() *RunAddressDataBuilder {
	b.setPersistent(UserLoginFailureAddr, nil)
	return b
}

func (b *RunAddressDataBuilder) WithFilePath(file string) *RunAddressDataBuilder {
	if file == "" {
		return b
	}
	b.setRASPEphemeral(ServerIOFSFileAddr, file)
	return b
}

func (b *RunAddressDataBuilder) WithDownwardMethod(method string) *RunAddressDataBuilder {
	if method == "" {
		return b
	}
	b.setEphemeral(ServerIONetRequestMethodAddr, method)
	return b
}

func (b *RunAddressDataBuilder) WithDownwardRequestHeaders(headers map[string][]string) *RunAddressDataBuilder {
	if len(headers) == 0 {
		return b
	}
	b.setEphemeral(ServerIONetRequestHeadersAddr, headers)
	return b
}

func (b *RunAddressDataBuilder) WithDownwardURL(url string) *RunAddressDataBuilder {
	if url == "" {
		return b
	}
	b.setRASPEphemeral(ServerIONetURLAddr, url)
	return b
}

func (b *RunAddressDataBuilder) WithDownwardRequestBody(body any) *RunAddressDataBuilder {
	if body == nil {
		return b
	}
	b.setEphemeral(ServerIONetRequestBodyAddr, body)
	return b
}

func (b *RunAddressDataBuilder) WithDownwardResponseStatus(status int) *RunAddressDataBuilder {
	if status == 0 {
		return b
	}
	b.setRASPEphemeral(ServerIONetResponseStatusAddr, strconv.Itoa(status))
	return b
}

func (b *RunAddressDataBuilder) WithDownwardResponseHeaders(headers map[string][]string) *RunAddressDataBuilder {
	if len(headers) == 0 {
		return b
	}
	b.setEphemeral(ServerIONetResponseHeadersAddr, headers)
	return b
}

func (b *RunAddressDataBuilder) WithDownwardResponseBody(body any) *RunAddressDataBuilder {
	if body == nil {
		return b
	}
	b.setEphemeral(ServerIONetResponseBodyAddr, body)
	return b
}

func (b *RunAddressDataBuilder) WithDBStatement(statement string) *RunAddressDataBuilder {
	if statement == "" {
		return b
	}
	b.setRASPEphemeral(ServerDBStatementAddr, statement)
	return b
}

func (b *RunAddressDataBuilder) WithDBType(driver string) *RunAddressDataBuilder {
	if driver == "" {
		return b
	}
	b.setRASPEphemeral(ServerDBTypeAddr, driver)
	return b
}

func (b *RunAddressDataBuilder) WithSysExecCmd(cmd []string) *RunAddressDataBuilder {
	if len(cmd) == 0 {
		return b
	}
	b.setRASPEphemeral(ServerSysExecCmd, cmd)
	return b
}

func (b *RunAddressDataBuilder) WithGRPCMethod(method string) *RunAddressDataBuilder {
	if method == "" {
		return b
	}
	b.setPersistent(GRPCServerMethodAddr, method)
	return b
}

func (b *RunAddressDataBuilder) WithGRPCRequestMessage(message any) *RunAddressDataBuilder {
	if message == nil {
		return b
	}
	b.setEphemeral(GRPCServerRequestMessageAddr, message)
	return b
}

func (b *RunAddressDataBuilder) WithGRPCRequestMetadata(metadata map[string][]string) *RunAddressDataBuilder {
	if len(metadata) == 0 {
		return b
	}
	b.setPersistent(GRPCServerRequestMetadataAddr, metadata)
	return b
}

func (b *RunAddressDataBuilder) WithGRPCResponseMessage(message any) *RunAddressDataBuilder {
	if message == nil {
		return b
	}
	b.setEphemeral(GRPCServerResponseMessageAddr, message)
	return b
}

func (b *RunAddressDataBuilder) WithGRPCResponseMetadataHeaders(headers map[string][]string) *RunAddressDataBuilder {
	if len(headers) == 0 {
		return b
	}
	b.setPersistent(GRPCServerResponseMetadataHeadersAddr, headers)
	return b
}

func (b *RunAddressDataBuilder) WithGRPCResponseMetadataTrailers(trailers map[string][]string) *RunAddressDataBuilder {
	if len(trailers) == 0 {
		return b
	}
	b.setPersistent(GRPCServerResponseMetadataTrailersAddr, trailers)
	return b
}

func (b *RunAddressDataBuilder) WithGRPCResponseStatusCode(status int) *RunAddressDataBuilder {
	if status == 0 {
		return b
	}
	b.setPersistent(GRPCServerResponseStatusCodeAddr, strconv.Itoa(status))
	return b
}

func (b *RunAddressDataBuilder) WithGraphQLResolver(fieldName string, args map[string]any) *RunAddressDataBuilder {
	if _, ok := b.data.Data[GraphQLServerResolverAddr]; !ok {
		b.setEphemeral(GraphQLServerResolverAddr, make(map[string]any, 1))
	}

	b.data.Data[GraphQLServerResolverAddr].(map[string]any)[fieldName] = args
	return b
}

func (b *RunAddressDataBuilder) ExtractSchema() *RunAddressDataBuilder {
	if _, ok := b.data.Data[contextProcessKey]; !ok {
		b.setPersistent(contextProcessKey, make(map[string]bool, 1))
	}

	b.data.Data[contextProcessKey].(map[string]bool)["extract-schema"] = true
	return b
}

func (b *RunAddressDataBuilder) NoExtractSchema() *RunAddressDataBuilder {
	if _, ok := b.data.Data[contextProcessKey]; !ok {
		b.setPersistent(contextProcessKey, make(map[string]bool, 1))
	}

	b.data.Data[contextProcessKey].(map[string]bool)["extract-schema"] = false
	return b
}

func (b *RunAddressDataBuilder) Build() RunAddressData {
	return b.data
}
