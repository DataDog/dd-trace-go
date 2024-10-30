// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package azure

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetHostname(t *testing.T) {
	ctx := context.Background()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"name": "vm-name",
			"resourceGroupName": "my-resource-group",
			"subscriptionId": "2370ac56-5683-45f8-a2d4-d1054292facb",
			"vmId": "b33fa46-6aff-4dfa-be0a-9e922ca3ac6d"
		}`)
	}))
	defer ts.Close()
	metadataURL = ts.URL

	cases := []struct {
		value string
		err   bool
	}{
		{"b33fa46-6aff-4dfa-be0a-9e922ca3ac6d", false},
	}

	for _, tt := range cases {
		hostname, err := GetHostname(ctx)
		assert.Equal(t, tt.value, hostname)
		assert.Equal(t, tt.err, err != nil)
	}
}

func TestGetHostnameWithInvalidMetadata(t *testing.T) {
	ctx := context.Background()

	for _, response := range []string{"", "!"} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, fmt.Sprintf(`{
				"name": "%s",
				"resourceGroupName": "%s",
				"subscriptionId": "%s",
				"vmId": "%s"
			}`, response, response, response, response))
		}))
		metadataURL = ts.URL

		t.Run(fmt.Sprintf("with response '%s'", response), func(t *testing.T) {
			hostname, err := GetHostname(ctx)
			assert.Empty(t, hostname)
			assert.NotNil(t, err)
		})

		ts.Close()
	}
}
