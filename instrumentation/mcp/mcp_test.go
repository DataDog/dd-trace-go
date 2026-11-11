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

func TestClientInfoTags(t *testing.T) {
	assert.Equal(t, map[string]string{"mcp_client_name": "cursor", "mcp_client_version": "1.2"}, ClientInfoTags("cursor", "", "1.2"))
	assert.Empty(t, ClientInfoTags("", "", ""))

	long := strings.Repeat("é", MaxClientInfoTagLength+5)
	assert.Equal(t, strings.Repeat("é", MaxClientInfoTagLength), ClientInfoTags(long, "", "")["mcp_client_name"])
}

func TestClientInfoTagsFromValue(t *testing.T) {
	want := map[string]string{"mcp_client_name": "cursor", "mcp_client_title": "Cursor", "mcp_client_version": "1.2"}
	assert.Equal(t, want, ClientInfoTagsFromValue(map[string]any{"name": "cursor", "title": "Cursor", "version": "1.2"}))
	assert.Equal(t, want, ClientInfoTagsFromValue(struct {
		Name    string `json:"name"`
		Title   string `json:"title,omitempty"`
		Version string `json:"version"`
	}{"cursor", "Cursor", "1.2"}))
	assert.Empty(t, ClientInfoTagsFromValue(nil))
	assert.Empty(t, ClientInfoTagsFromValue("not an object"))
}
