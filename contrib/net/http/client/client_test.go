// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	validURL   = "http://example.com"
	invalidURL = "http:/\x00/invalid."
)

func TestGet(t *testing.T) {
	ctx := context.Background()
	t.Run("valid URL", func(t *testing.T) {
		withMockDefaultClient(
			func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, ctx, req.Context())
				assert.Equal(t, "GET", req.Method)
				assert.Equal(t, validURL, req.URL.String())
				return &http.Response{StatusCode: 200}, nil
			},
			func() {
				res, err := Get(ctx, validURL)
				require.NoError(t, err)
				require.Equal(t, 200, res.StatusCode)
				res.Body.Close()
			},
		)
	})
	//nolint:bodyclose
	t.Run("invalid URL", func(t *testing.T) {
		withMockDefaultClient(
			func(*http.Request) (*http.Response, error) {
				assert.Fail(t, "unexpected call to RoundTrip")
				return nil, errors.New("unreachable")
			},
			func() {
				ctx := context.Background()
				res, err := Get(ctx, invalidURL)
				require.Error(t, err)
				require.Nil(t, res)
			},
		)
	})
}

func TestHead(t *testing.T) {
	t.Run("valid URL", func(t *testing.T) {
		ctx := context.Background()
		withMockDefaultClient(
			func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, ctx, req.Context())
				assert.Equal(t, "HEAD", req.Method)
				assert.Equal(t, validURL, req.URL.String())
				return &http.Response{StatusCode: 200}, nil
			},
			func() {
				res, err := Head(ctx, validURL)
				require.NoError(t, err)
				require.Equal(t, 200, res.StatusCode)
				res.Body.Close()
			},
		)
	})

	//nolint:bodyclose
	t.Run("invalid URL", func(t *testing.T) {
		withMockDefaultClient(
			func(*http.Request) (*http.Response, error) {
				assert.Fail(t, "unexpected call to RoundTrip")
				return nil, errors.New("unreachable")
			},
			func() {
				ctx := context.Background()
				res, err := Head(ctx, invalidURL)
				require.Error(t, err)
				require.Nil(t, res)
			},
		)
	})
}

func TestPost(t *testing.T) {
	const contentType = "text/plain"
	body := []byte("hello")

	t.Run("valid URL", func(t *testing.T) {
		ctx := context.Background()
		withMockDefaultClient(
			func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, ctx, req.Context())
				assert.Equal(t, "POST", req.Method)
				assert.Equal(t, validURL, req.URL.String())
				assert.Equal(t, contentType, req.Header.Get("content-type"))
				data, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				assert.Equal(t, body, data)
				return &http.Response{StatusCode: 200}, nil
			},
			func() {
				res, err := Post(ctx, validURL, contentType, bytes.NewReader(body))
				require.NoError(t, err)
				require.Equal(t, 200, res.StatusCode)
				res.Body.Close()
			},
		)
	})

	//nolint:bodyclose
	t.Run("invalid URL", func(t *testing.T) {
		withMockDefaultClient(
			func(*http.Request) (*http.Response, error) {
				assert.Fail(t, "unexpected call to RoundTrip")
				return nil, errors.New("unreachable")
			},
			func() {
				ctx := context.Background()
				res, err := Post(ctx, invalidURL, contentType, bytes.NewReader(body))
				require.Error(t, err)
				require.Nil(t, res)
			},
		)
	})
}

func TestPostForm(t *testing.T) {
	values := url.Values{
		"key": {"value1", "value2"},
		"foo": {"bar"},
	}

	t.Run("valid URL", func(t *testing.T) {
		ctx := context.Background()
		withMockDefaultClient(
			func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, ctx, req.Context())
				assert.Equal(t, "POST", req.Method)
				assert.Equal(t, validURL, req.URL.String())
				assert.Equal(t, "application/x-www-form-urlencoded", req.Header.Get("content-type"))
				data, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				assert.Equal(t, []byte(values.Encode()), data)
				return &http.Response{StatusCode: 200}, nil
			},
			func() {
				res, err := PostForm(ctx, validURL, values)
				require.NoError(t, err)
				require.Equal(t, 200, res.StatusCode)
				res.Body.Close()
			},
		)
	})

	//nolint:bodyclose
	t.Run("invalid URL", func(t *testing.T) {
		withMockDefaultClient(
			func(*http.Request) (*http.Response, error) {
				assert.Fail(t, "unexpected call to RoundTrip")
				return nil, errors.New("unreachable")
			},
			func() {
				ctx := context.Background()
				res, err := PostForm(ctx, invalidURL, values)
				require.Error(t, err)
				require.Nil(t, res)
			},
		)
	})
}

func withMockDefaultClient(roundTrip func(*http.Request) (*http.Response, error), cb func()) {
	backup := http.DefaultClient
	defer func() { http.DefaultClient = backup }()

	http.DefaultClient = &http.Client{Transport: testTransport(roundTrip)}
	cb()
}

type testTransport func(*http.Request) (*http.Response, error)

func (t testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t(req)
}
