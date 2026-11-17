// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package river

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	riverlib "github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// These tests run a real River client, with the SQLite driver so that they need
// no database server. They check what only a full client exercises: metadata
// surviving the database round trip, and a worker continuing the insert's trace.

type sortArgs struct {
	Strings []string `json:"strings"`
}

func (sortArgs) Kind() string { return "sort" }

type emailArgs struct {
	To string `json:"to"`
}

func (emailArgs) Kind() string { return "email" }

// workedJob is what a worker saw while working a job.
type workedJob struct {
	kind     string
	metadata []byte
	spanID   uint64
}

type testClient struct {
	*riverlib.Client[*sql.Tx]
	mu     sync.Mutex
	worked []workedJob
	events <-chan *riverlib.Event
}

func (c *testClient) record(ctx context.Context, row *rivertype.JobRow) {
	span, _ := tracer.SpanFromContext(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.worked = append(c.worked, workedJob{kind: row.Kind, metadata: row.Metadata, spanID: span.Context().SpanID()})
}

// waitEvents waits for n job events, and returns them.
func (c *testClient) waitEvents(t *testing.T, n int) []*riverlib.Event {
	t.Helper()
	var events []*riverlib.Event
	timeout := time.After(10 * time.Second)
	for len(events) < n {
		select {
		case ev := <-c.events:
			events = append(events, ev)
		case <-timeout:
			t.Fatalf("timed out waiting for %d job events, got %d", n, len(events))
		}
	}
	return events
}

func newTestClient(t *testing.T, sortWork func(context.Context, *riverlib.Job[sortArgs]) error, opts ...Option) *testClient {
	t.Helper()
	ctx := context.Background()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "river.sqlite3"))
	require.NoError(t, err)
	// SQLite allows a single writer; River's SQLite driver recommends one connection.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	driver := riversqlite.New(db)
	migrator, err := rivermigrate.New(driver, nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)

	tc := &testClient{}
	workers := riverlib.NewWorkers()
	riverlib.AddWorker(workers, riverlib.WorkFunc(func(ctx context.Context, job *riverlib.Job[sortArgs]) error {
		tc.record(ctx, job.JobRow)
		return sortWork(ctx, job)
	}))
	riverlib.AddWorker(workers, riverlib.WorkFunc(func(ctx context.Context, job *riverlib.Job[emailArgs]) error {
		tc.record(ctx, job.JobRow)
		return nil
	}))

	tc.Client, err = riverlib.NewClient(driver, &riverlib.Config{
		FetchCooldown:     10 * time.Millisecond,
		FetchPollInterval: 20 * time.Millisecond,
		Logger:            slog.New(slog.DiscardHandler),
		MaxAttempts:       1,
		Middleware:        []rivertype.Middleware{NewMiddleware(opts...)},
		Queues:            map[string]riverlib.QueueConfig{riverlib.QueueDefault: {MaxWorkers: 2}},
		TestOnly:          true,
		Workers:           workers,
	})
	require.NoError(t, err)

	events, cancel := tc.Subscribe(riverlib.EventKindJobCompleted, riverlib.EventKindJobFailed)
	t.Cleanup(cancel)
	tc.events = events

	require.NoError(t, tc.Start(ctx))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tc.Stop(stopCtx)
	})
	return tc
}

func spansByName(spans []*mocktracer.Span, name string) []*mocktracer.Span {
	var out []*mocktracer.Span
	for _, s := range spans {
		if s.OperationName() == name {
			out = append(out, s)
		}
	}
	return out
}

func TestClient(t *testing.T) {
	t.Run("work continues the insert trace", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		tc := newTestClient(t, func(context.Context, *riverlib.Job[sortArgs]) error { return nil })

		root, ctx := tracer.StartSpanFromContext(context.Background(), "http.request")
		root.SetTag(ext.ManualKeep, true)
		_, err := tc.Insert(ctx, sortArgs{Strings: []string{"b", "a"}}, &riverlib.InsertOpts{
			Metadata: []byte(`{"customer":"acme"}`),
		})
		require.NoError(t, err)
		root.Finish()
		tc.waitEvents(t, 1)
		require.NoError(t, tc.Stop(context.Background()))

		spans := mt.FinishedSpans()
		inserts := spansByName(spans, insertOperationName)
		works := spansByName(spans, workOperationName)
		require.Len(t, inserts, 1)
		require.Len(t, works, 1)
		insert, work := inserts[0], works[0]

		assert.Equal(t, root.Context().SpanID(), insert.ParentID())
		assert.Equal(t, insert.SpanID(), work.ParentID())
		assert.Equal(t, root.Context().TraceID(), work.Context().TraceID())
		assert.Equal(t, "sort", work.Tag(ext.ResourceName))
		assert.Equal(t, riverlib.QueueDefault, work.Tag(ext.MessagingDestinationName))
		assert.Nil(t, work.Tag(ext.ErrorMsg))

		// The sampling decision of the inserting request travels with the job.
		priority, ok := work.Context().SamplingPriority()
		require.True(t, ok)
		assert.Equal(t, ext.PriorityUserKeep, priority)

		// The worker runs inside the work span, and its own metadata is kept.
		require.Len(t, tc.worked, 1)
		assert.Equal(t, work.SpanID(), tc.worked[0].spanID)
		var metadata map[string]any
		require.NoError(t, json.Unmarshal(tc.worked[0].metadata, &metadata))
		assert.Equal(t, "acme", metadata["customer"])
		assert.Contains(t, metadata, metadataKey)
	})

	t.Run("batch insert", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		tc := newTestClient(t, func(context.Context, *riverlib.Job[sortArgs]) error { return nil })

		root, ctx := tracer.StartSpanFromContext(context.Background(), "root")
		_, err := tc.InsertMany(ctx, []riverlib.InsertManyParams{
			{Args: sortArgs{Strings: []string{"a"}}},
			{Args: sortArgs{Strings: []string{"b"}}},
			{Args: emailArgs{To: "a@example.com"}},
		})
		require.NoError(t, err)
		root.Finish()
		tc.waitEvents(t, 3)
		require.NoError(t, tc.Stop(context.Background()))

		spans := mt.FinishedSpans()
		inserts := spansByName(spans, insertOperationName)
		works := spansByName(spans, workOperationName)
		require.Len(t, inserts, 1)
		require.Len(t, works, 3)
		assert.Equal(t, resourceMultipleKinds, inserts[0].Tag(ext.ResourceName))
		assert.EqualValues(t, 3, inserts[0].Tag(tagMessagingBatchCount))
		kinds := map[string]int{}
		for _, w := range works {
			assert.Equal(t, inserts[0].SpanID(), w.ParentID())
			kinds[w.Tag(ext.ResourceName).(string)]++
		}
		assert.Equal(t, map[string]int{"sort": 2, "email": 1}, kinds)
	})

	t.Run("panicking worker", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		tc := newTestClient(t, func(context.Context, *riverlib.Job[sortArgs]) error { return panicInWorker() })

		_, err := tc.Insert(context.Background(), sortArgs{}, nil)
		require.NoError(t, err)
		events := tc.waitEvents(t, 1)
		require.NoError(t, tc.Stop(context.Background()))

		// River still recovers the panic, with a stack trace that points at the
		// worker rather than at the middleware that re-raised it.
		require.Equal(t, riverlib.EventKindJobFailed, events[0].Kind)
		require.Len(t, events[0].Job.Errors, 1)
		assert.Contains(t, events[0].Job.Errors[0].Error, "worker exploded")
		assert.Contains(t, events[0].Job.Errors[0].Trace, "panicInWorker")

		works := spansByName(mt.FinishedSpans(), workOperationName)
		require.Len(t, works, 1)
		assert.Equal(t, "panic: worker exploded", works[0].Tag(ext.ErrorMsg))
	})

	t.Run("jobs inserted without the middleware", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		tc := newTestClient(t, func(context.Context, *riverlib.Job[sortArgs]) error { return nil }, WithDistributedTracing(false))

		root, ctx := tracer.StartSpanFromContext(context.Background(), "root")
		_, err := tc.Insert(ctx, sortArgs{}, nil)
		require.NoError(t, err)
		root.Finish()
		tc.waitEvents(t, 1)
		require.NoError(t, tc.Stop(context.Background()))

		works := spansByName(mt.FinishedSpans(), workOperationName)
		require.Len(t, works, 1)
		assert.Equal(t, uint64(0), works[0].ParentID())
		assert.NotContains(t, string(tc.worked[0].metadata), metadataKey)
	})
}

//go:noinline
func panicInWorker() error {
	panic("worker exploded")
}
