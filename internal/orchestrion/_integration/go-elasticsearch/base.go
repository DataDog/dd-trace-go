// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/containers"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	testelasticsearch "github.com/testcontainers/testcontainers-go/modules/elasticsearch"
	"github.com/testcontainers/testcontainers-go/wait"
)

type esClient interface {
	Perform(*http.Request) (*http.Response, error)
}

type base struct {
	container *testelasticsearch.ElasticsearchContainer
	client    esClient
}

func (b *base) Setup(ctx context.Context, t *testing.T, image string, newClient func(addr string, caCert []byte) (esClient, error)) {
	containers.SkipIfProviderIsNotHealthy(t)

	parts := strings.Split(image, ":")
	require.Len(t, parts, 2)
	version := parts[1]
	major := strings.Split(version, ".")[0]
	containerName := "elasticsearch" + major

	var err error
	b.container, err = testelasticsearch.Run(ctx,
		image,
		testcontainers.WithEnv(map[string]string{
			"xpack.security.enabled": "false",
		}),
		testcontainers.WithLogger(tclog.TestLogger(t)),
		containers.WithTestLogConsumer(t),
		testcontainers.WithWaitStrategyAndDeadline(time.Minute, wait.ForLog(`.*("message":\s?"started(\s|")?.*|]\sstarted\n)`).AsRegexp()),
		// attempt to reuse this container
		testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     containerName,
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}),
	)
	containers.AssertTestContainersError(t, err)
	containers.RegisterContainerCleanup(t, b.container)

	b.client, err = newClient(b.container.Settings.Address, b.container.Settings.CACert)
	require.NoError(t, err)
}

func (b *base) Run(ctx context.Context, t *testing.T, doRequest func(t *testing.T, client esClient, body io.Reader)) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	data, err := json.Marshal(struct {
		Title string `json:"title"`
	}{Title: "some-title"})
	require.NoError(t, err)

	doRequest(t, b.client, bytes.NewReader(data))
}

func (*base) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "elasticsearch.query",
						"service":  "elastic.client",
						"resource": "PUT /test/_doc/?",
						"type":     "elasticsearch",
					},
					Meta: map[string]string{
						"component": "elastic/go-elasticsearch.v6",
						"span.kind": "client",
						"db.system": "elasticsearch",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"service":  "elastic.client",
								"resource": "PUT /test/_doc/*",
								"type":     "http",
							},
							Meta: map[string]string{
								"component": "net/http",
								"span.kind": "client",
							},
						},
					},
				},
			},
		},
	}
}
