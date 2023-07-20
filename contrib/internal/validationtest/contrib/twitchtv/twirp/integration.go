// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package twirp

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	twirptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/twitchtv/twirp"
)

type Integration struct {
	wc       twirptrace.HTTPClient
	numSpans int
	opts     []twirptrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]twirptrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "twitchtv/twirp"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	mc := &mockClient{code: 200}
	i.wc = twirptrace.WrapClient(mc, i.opts...)

	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	url := "http://localhost/twirp/twirp.test/Example/Method"
	req, err := http.NewRequest("POST", url, nil)
	require.NoError(t, err)
	_, err = i.wc.Do(req) //nolint:bodyclose
	require.NoError(t, err)
	i.numSpans++

}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, twirptrace.WithServiceName(name))
}

type mockClient struct {
	code int
	err  error
}

func (mc *mockClient) Do(req *http.Request) (*http.Response, error) {
	if mc.err != nil {
		return nil, mc.err
	}
	// the request body in a response should be nil based on the documentation of http.Response
	req.Body = nil
	res := &http.Response{
		Status:     fmt.Sprintf("%d %s", mc.code, http.StatusText(mc.code)),
		StatusCode: mc.code,
		Proto:      req.Proto,
		ProtoMajor: req.ProtoMajor,
		ProtoMinor: req.ProtoMinor,
		Request:    req,
	}
	return res, nil
}
