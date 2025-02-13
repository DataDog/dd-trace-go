// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package client provides [context.Context]-aware alternatives to the
// short-hand request functions [http.Get], [http.Head], [http.Post], and
// [http.PostForm]. Using these functions allows for better control over the
// trace context propagation.
package client

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Get is a [context.Context] aware version of [http.Get].
func Get(ctx context.Context, url string) (resp *http.Response, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// Head is a [context.Context] aware version of [http.Head].
func Head(ctx context.Context, url string) (resp *http.Response, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// Post is a [context.Context] aware version of [http.Post].
func Post(ctx context.Context, url string, contentType string, body io.Reader) (resp *http.Response, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return http.DefaultClient.Do(req)
}

// PostForm is a [context.Context] aware version of [http.PostForm].
func PostForm(ctx context.Context, url string, data url.Values) (resp *http.Response, err error) {
	return Post(ctx, url, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
}
