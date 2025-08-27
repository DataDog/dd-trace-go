// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"context"
	"io"
	"testing"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/stretchr/testify/require"
)

type TestCaseV8 struct {
	base
}

func (tc *TestCaseV8) Setup(ctx context.Context, t *testing.T) {
	// Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
	tc.base.Setup(ctx, t, "docker.elastic.co/elasticsearch/elasticsearch:8.15.3", func(addr string, caCert []byte) (esClient, error) {
		return elasticsearch.NewClient(elasticsearch.Config{
			Addresses: []string{addr},
		})
	})
}

func (tc *TestCaseV8) Run(ctx context.Context, t *testing.T) {
	tc.base.Run(ctx, t, func(t *testing.T, client esClient, body io.Reader) {
		t.Helper()
		req := esapi.IndexRequest{
			Index:      "test",
			DocumentID: "1",
			Body:       body,
			Refresh:    "true",
		}
		res, err := req.Do(ctx, client)
		require.NoError(t, err)
		defer res.Body.Close()
	})
}
