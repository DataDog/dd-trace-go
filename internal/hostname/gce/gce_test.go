// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package gce

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func reset() {
	hostnameFetcher.Reset()
	nameFetcher.Reset()
}

func TestGetHostname(t *testing.T) {
	reset()
	ctx := context.Background()
	expected := "gke-cluster-massi-agent59-default-pool-6087cc76-9cfa"
	var lastRequest *http.Request
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, expected)
		lastRequest = r
	}))
	defer ts.Close()
	metadataURL = ts.URL

	val, err := GetHostname(ctx)
	assert.Nil(t, err)
	assert.Equal(t, expected, val)
	assert.Equal(t, "/instance/hostname", lastRequest.URL.Path)
}

func TestGetHostnameEmptyBody(t *testing.T) {
	reset()
	ctx := context.Background()
	var lastRequest *http.Request
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		lastRequest = r
	}))
	defer ts.Close()
	metadataURL = ts.URL

	val, err := GetHostname(ctx)
	assert.Error(t, err)
	assert.Empty(t, val)
	assert.Equal(t, "/instance/hostname", lastRequest.URL.Path)
}
