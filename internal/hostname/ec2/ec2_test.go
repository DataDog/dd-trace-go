// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package ec2

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	initialMetadataURL = metadataURL
)

func resetPackageVars() {
	metadataURL = initialMetadataURL
	instanceIDFetcher.Reset()
}

func TestIsDefaultHostname(t *testing.T) {
	assert.True(t, IsDefaultHostname("IP-FOO"))
	assert.True(t, IsDefaultHostname("domuarigato"))
	assert.True(t, IsDefaultHostname("EC2AMAZ-FOO"))
	assert.False(t, IsDefaultHostname(""))
}

func TestGetInstanceID(t *testing.T) {
	ctx := context.Background()
	expected := "i-0123456789abcdef0"
	var responseCode int
	var lastRequest *http.Request
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(responseCode)
		io.WriteString(w, expected)
		lastRequest = r
	}))
	defer ts.Close()
	metadataURL = ts.URL
	defer resetPackageVars()

	// API errors out, should return error
	responseCode = http.StatusInternalServerError
	val, err := GetInstanceID(ctx)
	assert.NotNil(t, err)
	assert.Equal(t, "", val)
	assert.Equal(t, lastRequest.URL.Path, "/instance-id")

	// API successful, should return API result
	responseCode = http.StatusOK
	val, err = GetInstanceID(ctx)
	assert.Nil(t, err)
	assert.Equal(t, expected, val)
	assert.Equal(t, lastRequest.URL.Path, "/instance-id")

	// the internal cache is populated now, should return the cached value even if API errors out
	responseCode = http.StatusInternalServerError
	val, err = GetInstanceID(ctx)
	assert.Nil(t, err)
	assert.Equal(t, expected, val)
	assert.Equal(t, lastRequest.URL.Path, "/instance-id")

	// the internal cache is populated, should refresh result if API call succeeds
	responseCode = http.StatusOK
	expected = "i-aaaaaaaaaaaaaaaaa"
	val, err = GetInstanceID(ctx)
	assert.Nil(t, err)
	assert.Equal(t, expected, val)
	assert.Equal(t, lastRequest.URL.Path, "/instance-id")
}
