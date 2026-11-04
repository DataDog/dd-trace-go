// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package mongo

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	mongotrace "github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver.v2/v2/mongo"
	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

const component = "go.mongodb.org/mongo-driver.v2"

// TestMonitorSpanCount pins how the injected monitor combines with monitors the
// application sets itself, by counting the spans of a single insert.
func TestMonitorSpanCount(t *testing.T) {
	require.True(t, built.WithOrchestrion || otelc.Enabled(),
		"this test must be run with either orchestrion or otelc enabled")
	containers.SkipIfProviderIsNotHealthy(t)
	_, uri := containers.StartMongoDBTestContainer(t)

	for _, tc := range []struct {
		name string
		opts func(user *event.CommandMonitor) []*options.ClientOptions
		// spans is the number of traced inserts, and userEvents the number of
		// inserts the application's own monitor saw.
		spans, userEvents int
	}{
		{
			name: "injected",
			opts: func(*event.CommandMonitor) []*options.ClientOptions {
				return []*options.ClientOptions{options.Client().ApplyURI(uri)}
			},
			spans: 1,
		},
		{
			// The application's monitor replaces the injected one.
			name: "contrib monitor",
			opts: func(*event.CommandMonitor) []*options.ClientOptions {
				return []*options.ClientOptions{options.Client().ApplyURI(uri).SetMonitor(mongotrace.NewMonitor())}
			},
			spans: 1,
		},
		{
			name: "user monitor",
			opts: func(user *event.CommandMonitor) []*options.ClientOptions {
				return []*options.ClientOptions{options.Client().ApplyURI(uri).SetMonitor(user)}
			},
			userEvents: 1,
		},
		{
			// The last options with a monitor win when they are merged.
			name: "merged",
			opts: func(*event.CommandMonitor) []*options.ClientOptions {
				return []*options.ClientOptions{options.Client().ApplyURI(uri), options.Client().SetAppName("merged")}
			},
			spans: 1,
		},
		{
			// FIXME: the later options.Client() carries the contrib's monitor, so the
			// user monitor is lost. Orchestrion behaves the same.
			name: "merged after user monitor",
			opts: func(user *event.CommandMonitor) []*options.ClientOptions {
				return []*options.ClientOptions{options.Client().ApplyURI(uri).SetMonitor(user), options.Client().SetAppName("merged")}
			},
			spans: 1,
		},
		{
			name: "merged before user monitor",
			opts: func(user *event.CommandMonitor) []*options.ClientOptions {
				return []*options.ClientOptions{options.Client().ApplyURI(uri), options.Client().SetMonitor(user)}
			},
			userEvents: 1,
		},
		{
			// No options.Client() call site, so no monitor. The driver's own
			// Client() call in MergeClientOptions is not instrumented either.
			name: "literal",
			opts: func(*event.CommandMonitor) []*options.ClientOptions {
				return []*options.ClientOptions{(&options.ClientOptions{}).ApplyURI(uri), {}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, agent, err := tracertest.Bootstrap(t,
				tracer.WithSampler(tracer.NewAllSampler()),
				tracer.WithLogStartup(false),
			)
			require.NoError(t, err)

			var userEvents atomic.Int32
			user := &event.CommandMonitor{
				Started: func(_ context.Context, evt *event.CommandStartedEvent) {
					if evt.CommandName == "insert" {
						userEvents.Add(1)
					}
				},
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			client, err := mongo.Connect(tc.opts(user)...)
			require.NoError(t, err)
			_, err = client.Database("test").Collection("span_count").InsertOne(ctx, bson.M{"case": tc.name})
			require.NoError(t, err)
			require.NoError(t, client.Disconnect(ctx))
			tr.Stop()

			assert.Equal(t, tc.spans, insertsByDB(t, agent)["test"], "traced inserts")
			assert.Equal(t, tc.userEvents, int(userEvents.Load()), "inserts seen by the user monitor")
		})
	}
}

// insertsByDB counts the traced inserts in each database.
func insertsByDB(t *testing.T, agent agenttest.Agent) map[string]int {
	t.Helper()
	lister, ok := agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	inserts := map[string]int{}
	for _, span := range lister.Spans() {
		if span.Meta["component"] == component && span.Resource == "mongo.insert" {
			inserts[span.Meta["db.instance"]]++
		}
	}
	return inserts
}
