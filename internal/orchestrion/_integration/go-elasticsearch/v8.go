// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"testing"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/internal/orchestrion/_integration/internal/containers"
)

type TestCaseV8 struct {
	base
}

func (tc *TestCaseV8) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	var err error
	tc.container = containers.StartElasticsearchV8Container(t)

	// from v8, there's a certificate configured by default.
	// we cannot configure directly in the elasticsearch.Config type as it makes a type assertion on the underlying
	// transport type, which fails for the *elastictrace.roundTripper type from our instrumentation package.
	// https://github.com/elastic/elastic-transport-go/blob/main/elastictransport/elastictransport.go#L188-L191
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(tc.container.Settings.CACert)

	tc.client, err = elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{tc.container.Settings.Address},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: caCertPool,
			},
		},
	})
	require.NoError(t, err)
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
