// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package elastic7

import (
	"context"
	"strings"
	"testing"

	elasticsearch7 "github.com/elastic/go-elasticsearch/v7"
	esapi7 "github.com/elastic/go-elasticsearch/v7/esapi"
	"github.com/stretchr/testify/assert"
	elastictrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/elastic/go-elasticsearch.v6"
)

const (
	elasticV6URL = "http://127.0.0.1:9202"
	elasticV7URL = "http://127.0.0.1:9203"
	elasticV8URL = "http://127.0.0.1:9204"
)

type Integration struct {
	client   *elasticsearch7.Client
	numSpans int
}

func New() *Integration {
	return &Integration{}
}

func (i *Integration) ResetNumSpans() {
	i.numSpans = 0
}

func (i *Integration) Name() string {
	return "contrib/elastic/go-elasticsearch.v7"
}

func (i *Integration) Init(t *testing.T) func() {
	t.Helper()
	cfg := elasticsearch7.Config{
		Transport: elastictrace.NewRoundTripper(),
		Addresses: []string{
			elasticV6URL,
		},
	}
	var err error
	i.client, err = elasticsearch7.NewClient(cfg)
	assert.NoError(t, err)

	return func() {}
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()
	assert := assert.New(t)

	var err error
	_, err = esapi7.IndexRequest{
		Index:        "twitter",
		DocumentID:   "1",
		DocumentType: "tweet",
		Body:         strings.NewReader(`{"user": "test", "message": "hello"}`),
	}.Do(context.Background(), i.client)
	assert.NoError(err)
	i.numSpans += 2

	_, err = esapi7.GetRequest{
		Index:        "twitter",
		DocumentID:   "1",
		DocumentType: "tweet",
	}.Do(context.Background(), i.client)
	assert.NoError(err)
	i.numSpans++

	_, err = esapi7.GetRequest{
		Index:      "not-real-index",
		DocumentID: "1",
	}.Do(context.Background(), i.client)
	assert.NoError(err)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}
