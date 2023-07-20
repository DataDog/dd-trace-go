// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/books/v1"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"

	apitrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/api"
)

type Integration struct {
	svc      *books.Service
	client   *http.Client
	numSpans int
	opts     []apitrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]apitrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "google.golang.org/api"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	var err error
	i.svc, err = books.NewService(context.Background(), option.WithHTTPClient(&http.Client{
		Transport: apitrace.WrapRoundTripper(badRequestTransport, i.opts...),
	}))
	require.NoError(t, err)

	i.opts = append(i.opts, apitrace.WithScopes(cloudresourcemanager.CloudPlatformScope))
	i.client, _ = apitrace.NewClient(i.opts...)

	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	i.svc.Bookshelves.List("montana.banana").Do()
	i.numSpans++

	svc, _ := cloudresourcemanager.New(i.client)
	_, _ = svc.Projects.List().Do()
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, apitrace.WithServiceName(name))
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

var badRequestTransport roundTripperFunc = func(req *http.Request) (*http.Response, error) {
	res := &http.Response{
		Header:     make(http.Header),
		Request:    req,
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	return res, nil
}
