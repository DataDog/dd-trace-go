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

	"github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v7/esapi"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/containers"
)

type TestCaseV7 struct {
	base
}

func (tc *TestCaseV7) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	var err error
	tc.container = containers.StartElasticsearchV7Container(t)
	tc.client, err = elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{tc.container.Settings.Address},
	})
	require.NoError(t, err)
}

func (tc *TestCaseV7) Run(ctx context.Context, t *testing.T) {
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
