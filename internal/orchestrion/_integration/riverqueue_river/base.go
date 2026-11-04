// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package riverqueue_river

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// The suite uses River's SQLite driver so that it needs no database server.

type sortArgs struct {
	Strings []string `json:"strings"`
}

func (sortArgs) Kind() string { return "sort" }

type sortWorker struct {
	river.WorkerDefaults[sortArgs]
}

func (*sortWorker) Work(context.Context, *river.Job[sortArgs]) error { return nil }

// base holds a started River client and the events of the jobs it works.
type base struct {
	client *river.Client[*sql.Tx]
	events <-chan *river.Event
	cancel func()
}

func openDriver(t *testing.T) *riversqlite.Driver {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "river.sqlite3"))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	driver := riversqlite.New(db)
	migrator, err := rivermigrate.New(driver, nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(context.Background(), rivermigrate.DirectionUp, nil)
	require.NoError(t, err)
	return driver
}

func baseConfig() *river.Config {
	workers := river.NewWorkers()
	river.AddWorker(workers, &sortWorker{})
	return &river.Config{
		FetchCooldown:     10 * time.Millisecond,
		FetchPollInterval: 20 * time.Millisecond,
		Logger:            slog.New(slog.DiscardHandler),
		Queues:            map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
		TestOnly:          true,
		Workers:           workers,
	}
}

func (b *base) start(ctx context.Context, t *testing.T, client *river.Client[*sql.Tx]) {
	t.Helper()
	b.client = client
	b.events, b.cancel = client.Subscribe(river.EventKindJobCompleted)
	require.NoError(t, client.Start(ctx))
	t.Cleanup(func() {
		b.cancel()
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(stopCtx)
	})
}

func (b *base) wait(t *testing.T, n int) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for range n {
		select {
		case <-b.events:
		case <-timeout:
			t.Fatal("timed out waiting for River to work the job")
		}
	}
}

// insertAndWorkTrace is the trace of a job inserted under a root span and worked.
func insertAndWorkTrace() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{"name": "test.root"},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "river.insert_many",
						"resource": "sort",
						"type":     "queue",
					},
					Meta: map[string]string{
						"component":                  "riverqueue/river",
						"span.kind":                  "producer",
						"messaging.system":           "river",
						"messaging.destination.name": "default",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "river.work",
								"resource": "sort",
								"type":     "queue",
							},
							Meta: map[string]string{
								"component":                  "riverqueue/river",
								"span.kind":                  "consumer",
								"messaging.system":           "river",
								"messaging.destination.name": "default",
								"river.job.kind":             "sort",
							},
						},
					},
				},
			},
		},
	}
}
