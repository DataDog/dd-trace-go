// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripReservedMeta(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no meta",
			in:   `{"method":"tools/call","params":{"name":"echo"}}`,
			want: `{"method":"tools/call","params":{"name":"echo"}}`,
		},
		{
			name: "only reserved keys drops meta",
			in:   `{"method":"tools/call","params":{"name":"echo","_meta":{"io.modelcontextprotocol/clientInfo":{"name":"c"},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			want: `{"method":"tools/call","params":{"name":"echo"}}`,
		},
		{
			name: "keeps other meta keys",
			in:   `{"method":"tools/call","params":{"name":"echo","_meta":{"io.modelcontextprotocol/clientInfo":{"name":"c"},"progressToken":7}}}`,
			want: `{"method":"tools/call","params":{"_meta":{"progressToken":7},"name":"echo"}}`,
		},
		{
			name: "keeps exact encoding of other values",
			in:   `{"method":"tools/call","params":{"arguments":{"id":12345678901234567890},"_meta":{"io.modelcontextprotocol/clientInfo":{}}}}`,
			want: `{"method":"tools/call","params":{"arguments":{"id":12345678901234567890}}}`,
		},
		{
			name: "reserved prefix outside meta is unchanged",
			in:   `{"method":"tools/call","params":{"arguments":{"q":"io.modelcontextprotocol/x"}}}`,
			want: `{"method":"tools/call","params":{"arguments":{"q":"io.modelcontextprotocol/x"}}}`,
		},
		{
			name: "invalid json is unchanged",
			in:   `{"io.modelcontextprotocol/`,
			want: `{"io.modelcontextprotocol/`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, string(StripReservedMeta([]byte(tc.in))))
		})
	}
}

func TestClientInfoTags(t *testing.T) {
	assert.Equal(t, map[string]string{"mcp_client_name": "cursor", "mcp_client_version": "1.2"}, ClientInfoTags("cursor", "", "1.2"))
	assert.Empty(t, ClientInfoTags("", "", ""))

	long := strings.Repeat("é", MaxClientInfoTagLength+5)
	assert.Equal(t, strings.Repeat("é", MaxClientInfoTagLength), ClientInfoTags(long, "", "")["mcp_client_name"])
}
