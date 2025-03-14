package river

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const (
	postgresDSN = "postgres://postgres:postgres@127.0.0.1:5433/postgres?sslmode=disable"
)

func prepareDB() (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	pool, err := newPool(ctx)
	if err != nil {
		return nil, err
	}

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return nil, err
	}
	if err := migrateDrop(ctx, migrator); err != nil {
		return nil, err
	}
	if err := migrateToLatest(ctx, migrator); err != nil {
		return nil, err
	}

	return func() {
		defer pool.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		if err := migrateDrop(ctx, migrator); err != nil {
			log.Println(err)
		}
	}, nil
}

func newPool(ctx context.Context) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(postgresDSN)
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

func migrateToLatest(ctx context.Context, migrator *rivermigrate.Migrator[pgx.Tx]) error {
	_, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, &rivermigrate.MigrateOpts{
		TargetVersion: 6,
	})
	return err
}

func migrateDrop(ctx context.Context, migrator *rivermigrate.Migrator[pgx.Tx]) error {
	_, err := migrator.Migrate(ctx, rivermigrate.DirectionDown, &rivermigrate.MigrateOpts{
		TargetVersion: -1,
	})
	return err
}

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		log.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	cleanup, err := prepareDB()
	if err != nil {
		log.Fatal(err)
	}

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func lowerEqual(t *testing.T, id uint64, tid [16]byte) {
	assert.Equal(t, id, binary.BigEndian.Uint64(tid[8:]))
}

func metadataToMap(t *testing.T, v []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(v, &m); err != nil {
		t.Errorf("error unmarshal metadata: %v", err)
	}
	return m
}

func TestPropagation(t *testing.T) {
	ctx, mt, driver := setup(t)

	var (
		called      = false
		spanID      uint64
		jobMetadata []byte
	)
	worker := testWorker{f: func(ctx context.Context, job *river.Job[jobArg]) error {
		assert.False(t, called, "work called twice")
		assert.Equal(t, "data", job.Args.Data)
		span, ok := tracer.SpanFromContext(ctx)
		assert.True(t, ok, "no span")
		lowerEqual(t, uint64(42), span.Context().TraceIDBytes())
		spanID = span.Context().SpanID()
		jobMetadata = job.Metadata
		called = true
		return nil
	}}
	workers := river.NewWorkers()
	require.NoError(t, river.AddWorkerSafely(workers, worker))

	client, err := river.NewClient(driver, &river.Config{
		TestOnly:            true,
		MaxAttempts:         1,
		JobTimeout:          1 * time.Second,
		JobInsertMiddleware: []rivertype.JobInsertMiddleware{NewInsertMiddleware()},
		Workers:             workers,
		WorkerMiddleware:    []rivertype.WorkerMiddleware{NewWorkerMiddleware()},
		Queues:              map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	require.NoError(t, err)
	t.Cleanup(stopClientF(t, client))

	span, insertCtx := tracer.StartSpanFromContext(ctx, "propagation-test", tracer.WithSpanID(42))
	_, err = client.Insert(insertCtx, jobArg{Data: "data"}, &river.InsertOpts{})
	assert.NoError(t, err)
	span.Finish()

	events, _ := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	require.NoError(t, client.Start(context.Background()))
	select {
	case event := <-events:
		assert.Equal(t, river.EventKindJobCompleted, event.Kind)
		assert.True(t, called, "work not called")
	case <-ctx.Done():
		require.Fail(t, "did not receive event before timeout")
	}

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 3, "wrong number of spans")
	assert.Equal(t, "river.send", spans[0].OperationName())
	assert.Equal(t, "propagation-test", spans[1].OperationName())
	assert.Equal(t, "river.process", spans[2].OperationName())

	s0 := spans[0]
	assert.Equal(t, spans[1].SpanID(), s0.ParentID())
	assert.Equal(t, uint64(42), s0.TraceID())
	assert.Equal(t, ext.SpanTypeMessageProducer, s0.Tag(ext.SpanType))
	assert.Equal(t, "riverqueue/river", s0.Tag(ext.Component))
	assert.Equal(t, "riverqueue/river", s0.Integration())
	assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "river", s0.Tag(ext.MessagingSystem))
	assert.Equal(t, "river", s0.Tag(ext.ServiceName))
	assert.Equal(t, "river.insert_many", s0.Tag(ext.ResourceName))
	assert.Equal(t, "river.send", s0.Tag(ext.SpanName))

	s2 := spans[2]
	assert.Equal(t, s0.SpanID(), s2.ParentID())
	assert.Equal(t, uint64(42), s2.TraceID())
	assert.Equal(t, spanID, s2.SpanID())
	assert.Equal(t, ext.SpanTypeMessageConsumer, s2.Tag(ext.SpanType))
	assert.Equal(t, "riverqueue/river", s2.Tag(ext.Component))
	assert.Equal(t, "riverqueue/river", s2.Integration())
	assert.Equal(t, ext.SpanKindConsumer, s2.Tag(ext.SpanKind))
	assert.Equal(t, "river", s2.Tag(ext.MessagingSystem))
	assert.Equal(t, "river", s2.Tag(ext.ServiceName))
	assert.Equal(t, "kind", s2.Tag(ext.ResourceName))
	assert.Equal(t, "river.process", s2.Tag(ext.SpanName))
	assert.Equal(t, "default", s2.Tag("river_job.queue"))
	assert.Equal(t, "kind", s2.Tag("river_job.kind"))
	assert.Equal(t, float64(1), s2.Tag("river_job.attempt"))

	meta := metadataToMap(t, jobMetadata)
	assert.Equal(t, strconv.FormatUint(s0.SpanID(), 10), meta["x-datadog-parent-id"])
	assert.Equal(t, strconv.FormatUint(s0.TraceID(), 10), meta["x-datadog-trace-id"])
}

func TestPropagationWithService(t *testing.T) {
	ctx, mt, driver := setup(t)

	worker := testWorker{f: func(ctx context.Context, job *river.Job[jobArg]) error { return nil }}
	workers := river.NewWorkers()
	require.NoError(t, river.AddWorkerSafely(workers, worker))

	client, err := river.NewClient(driver, &river.Config{
		TestOnly:            true,
		MaxAttempts:         1,
		JobTimeout:          1 * time.Second,
		JobInsertMiddleware: []rivertype.JobInsertMiddleware{NewInsertMiddleware(WithService("insert.service"))},
		Workers:             workers,
		WorkerMiddleware:    []rivertype.WorkerMiddleware{NewWorkerMiddleware(WithService("worker.service"))},
		Queues:              map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	require.NoError(t, err)
	t.Cleanup(stopClientF(t, client))

	span, insertCtx := tracer.StartSpanFromContext(ctx, "service-name-test", tracer.WithSpanID(42))
	_, err = client.Insert(insertCtx, jobArg{Data: "data"}, &river.InsertOpts{})
	assert.NoError(t, err)
	span.Finish()

	events, _ := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	require.NoError(t, client.Start(context.Background()))
	select {
	case event := <-events:
		assert.Equal(t, river.EventKindJobCompleted, event.Kind)
	case <-ctx.Done():
		require.Fail(t, "did not receive event before timeout")
	}

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 3, "wrong number of spans")
	assert.Equal(t, "insert.service", spans[0].Tag(ext.ServiceName))
	assert.Equal(t, "worker.service", spans[2].Tag(ext.ServiceName))
}

func TestPropagationNoParentSpan(t *testing.T) {
	ctx, mt, driver := setup(t)

	var (
		called      = false
		spanID      uint64
		traceID     string
		jobMetadata []byte
	)
	worker := testWorker{f: func(ctx context.Context, job *river.Job[jobArg]) error {
		assert.False(t, called, "work called twice")
		assert.Equal(t, "data", job.Args.Data)
		span, ok := tracer.SpanFromContext(ctx)
		assert.True(t, ok, "no span")
		spanID = span.Context().SpanID()
		traceID = span.Context().TraceID()
		jobMetadata = job.Metadata
		called = true
		return nil
	}}
	workers := river.NewWorkers()
	require.NoError(t, river.AddWorkerSafely(workers, worker))

	client, err := river.NewClient(driver, &river.Config{
		TestOnly:            true,
		MaxAttempts:         1,
		JobTimeout:          1 * time.Second,
		JobInsertMiddleware: []rivertype.JobInsertMiddleware{NewInsertMiddleware()},
		Workers:             workers,
		WorkerMiddleware:    []rivertype.WorkerMiddleware{NewWorkerMiddleware()},
		Queues:              map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	require.NoError(t, err)
	t.Cleanup(stopClientF(t, client))

	// no parent span
	_, err = client.Insert(ctx, jobArg{Data: "data"}, &river.InsertOpts{})
	assert.NoError(t, err)

	events, _ := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	require.NoError(t, client.Start(context.Background()))
	select {
	case event := <-events:
		assert.Equal(t, river.EventKindJobCompleted, event.Kind)
		assert.True(t, called, "work not called")
	case <-ctx.Done():
		require.Fail(t, "did not receive event before timeout")
	}

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2, "wrong number of spans")
	assert.Equal(t, "river.send", spans[0].OperationName())
	assert.Equal(t, "river.process", spans[1].OperationName())

	s0 := spans[0]
	assert.Equal(t, s0.TraceID(), s0.SpanID())
	assert.Equal(t, traceID, s0.Context().TraceID())
	assert.Equal(t, ext.SpanTypeMessageProducer, s0.Tag(ext.SpanType))
	assert.Equal(t, "riverqueue/river", s0.Tag(ext.Component))
	assert.Equal(t, "riverqueue/river", s0.Integration())
	assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "river", s0.Tag(ext.MessagingSystem))
	assert.Equal(t, "river", s0.Tag(ext.ServiceName))
	assert.Equal(t, "river.insert_many", s0.Tag(ext.ResourceName))
	assert.Equal(t, "river.send", s0.Tag(ext.SpanName))

	s1 := spans[1]
	assert.Equal(t, s0.SpanID(), s1.ParentID())
	assert.Equal(t, traceID, s1.Context().TraceID())
	assert.Equal(t, spanID, s1.SpanID())
	assert.Equal(t, ext.SpanTypeMessageConsumer, s1.Tag(ext.SpanType))
	assert.Equal(t, "riverqueue/river", s1.Tag(ext.Component))
	assert.Equal(t, "riverqueue/river", s1.Integration())
	assert.Equal(t, ext.SpanKindConsumer, s1.Tag(ext.SpanKind))
	assert.Equal(t, "river", s1.Tag(ext.MessagingSystem))
	assert.Equal(t, "river", s1.Tag(ext.ServiceName))
	assert.Equal(t, "kind", s1.Tag(ext.ResourceName))
	assert.Equal(t, "river.process", s1.Tag(ext.SpanName))
	assert.Equal(t, "default", s1.Tag("river_job.queue"))
	assert.Equal(t, "kind", s1.Tag("river_job.kind"))
	assert.Equal(t, float64(1), s1.Tag("river_job.attempt"))

	meta := metadataToMap(t, jobMetadata)
	assert.Equal(t, strconv.FormatUint(s0.SpanID(), 10), meta["x-datadog-parent-id"])
	assert.Equal(t, strconv.FormatUint(s0.TraceID(), 10), meta["x-datadog-trace-id"])
}

func TestPropagationNoInsertSpan(t *testing.T) {
	ctx, mt, driver := setup(t)

	var (
		called      = false
		spanID      uint64
		traceID     string
		jobMetadata string
	)
	worker := testWorker{f: func(ctx context.Context, job *river.Job[jobArg]) error {
		assert.False(t, called, "work called twice")
		assert.Equal(t, "data", job.Args.Data)
		span, ok := tracer.SpanFromContext(ctx)
		assert.True(t, ok, "no span")
		spanID = span.Context().SpanID()
		traceID = span.Context().TraceID()
		jobMetadata = string(job.Metadata)
		called = true
		return nil
	}}
	workers := river.NewWorkers()
	require.NoError(t, river.AddWorkerSafely(workers, worker))

	client, err := river.NewClient(driver, &river.Config{
		TestOnly:            true,
		MaxAttempts:         1,
		JobTimeout:          1 * time.Second,
		JobInsertMiddleware: nil, // no tracing on JobInsert
		Workers:             workers,
		WorkerMiddleware:    []rivertype.WorkerMiddleware{NewWorkerMiddleware()},
		Queues:              map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	require.NoError(t, err)
	t.Cleanup(stopClientF(t, client))

	_, err = client.Insert(ctx, jobArg{Data: "data"}, &river.InsertOpts{})
	assert.NoError(t, err)

	events, _ := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	require.NoError(t, client.Start(context.Background()))
	select {
	case event := <-events:
		assert.Equal(t, river.EventKindJobCompleted, event.Kind)
		assert.True(t, called, "work not called")
	case <-ctx.Done():
		require.Fail(t, "did not receive event before timeout")
	}

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1, "wrong number of spans")
	assert.Equal(t, "river.process", spans[0].OperationName())

	s0 := spans[0]
	assert.Equal(t, traceID, s0.Context().TraceID())
	assert.Equal(t, spanID, s0.SpanID())
	assert.Equal(t, ext.SpanTypeMessageConsumer, s0.Tag(ext.SpanType))
	assert.Equal(t, "riverqueue/river", s0.Tag(ext.Component))
	assert.Equal(t, "riverqueue/river", s0.Integration())
	assert.Equal(t, ext.SpanKindConsumer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "river", s0.Tag(ext.MessagingSystem))
	assert.Equal(t, "river", s0.Tag(ext.ServiceName))
	assert.Equal(t, "kind", s0.Tag(ext.ResourceName))
	assert.Equal(t, "river.process", s0.Tag(ext.SpanName))
	assert.Equal(t, "default", s0.Tag("river_job.queue"))
	assert.Equal(t, "kind", s0.Tag("river_job.kind"))
	assert.Equal(t, float64(1), s0.Tag("river_job.attempt"))

	assert.Equal(t, `{}`, jobMetadata)
}

func TestWorkerError(t *testing.T) {
	ctx, mt, driver := setup(t)

	var (
		called      = false
		spanID      uint64
		traceID     string
		jobMetadata string
		workErr     = errors.New("worker error")
	)
	worker := testWorker{f: func(ctx context.Context, job *river.Job[jobArg]) error {
		assert.False(t, called, "work called twice")
		assert.Equal(t, "data", job.Args.Data)
		span, ok := tracer.SpanFromContext(ctx)
		assert.True(t, ok, "no span")
		spanID = span.Context().SpanID()
		traceID = span.Context().TraceID()
		jobMetadata = string(job.Metadata)
		called = true
		return workErr
	}}
	workers := river.NewWorkers()
	require.NoError(t, river.AddWorkerSafely(workers, worker))

	client, err := river.NewClient(driver, &river.Config{
		TestOnly:            true,
		MaxAttempts:         1,
		JobTimeout:          1 * time.Second,
		JobInsertMiddleware: nil,
		Workers:             workers,
		WorkerMiddleware:    []rivertype.WorkerMiddleware{NewWorkerMiddleware()},
		Queues:              map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	require.NoError(t, err)
	t.Cleanup(stopClientF(t, client))

	_, err = client.Insert(ctx, jobArg{Data: "data"}, &river.InsertOpts{})
	assert.NoError(t, err)

	events, _ := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	require.NoError(t, client.Start(context.Background()))
	select {
	case event := <-events:
		assert.Equal(t, river.EventKindJobFailed, event.Kind)
		assert.True(t, called, "work not called")
	case <-ctx.Done():
		require.Fail(t, "did not receive event before timeout")
	}

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1, "wrong number of spans")
	assert.Equal(t, "river.process", spans[0].OperationName())

	s0 := spans[0]
	assert.Equal(t, traceID, s0.Context().TraceID())
	assert.Equal(t, spanID, s0.SpanID())
	assert.Equal(t, ext.SpanTypeMessageConsumer, s0.Tag(ext.SpanType))
	assert.Equal(t, "riverqueue/river", s0.Tag(ext.Component))
	assert.Equal(t, "riverqueue/river", s0.Integration())
	assert.Equal(t, ext.SpanKindConsumer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "river", s0.Tag(ext.MessagingSystem))
	assert.Equal(t, "river", s0.Tag(ext.ServiceName))
	assert.Equal(t, "kind", s0.Tag(ext.ResourceName))
	assert.Equal(t, "river.process", s0.Tag(ext.SpanName))
	assert.Equal(t, "default", s0.Tag("river_job.queue"))
	assert.Equal(t, "kind", s0.Tag("river_job.kind"))
	assert.Equal(t, float64(1), s0.Tag("river_job.attempt"))
	assert.Equal(t, workErr.Error(), s0.Tag(ext.ErrorMsg))

	assert.Equal(t, `{}`, jobMetadata)
}

func TestAdditionalMetadata(t *testing.T) {
	ctx, mt, driver := setup(t)

	var (
		jobMetadata []byte
	)
	worker := testWorker{f: func(ctx context.Context, job *river.Job[jobArg]) error {
		jobMetadata = job.Metadata
		return nil
	}}
	workers := river.NewWorkers()
	require.NoError(t, river.AddWorkerSafely(workers, worker))

	client, err := river.NewClient(driver, &river.Config{
		TestOnly:            true,
		MaxAttempts:         1,
		JobTimeout:          1 * time.Second,
		JobInsertMiddleware: []rivertype.JobInsertMiddleware{NewInsertMiddleware()},
		Workers:             workers,
		WorkerMiddleware:    []rivertype.WorkerMiddleware{NewWorkerMiddleware()},
		Queues:              map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	require.NoError(t, err)
	t.Cleanup(stopClientF(t, client))

	_, err = client.Insert(ctx, jobArg{Data: "data"}, &river.InsertOpts{
		Metadata: []byte(`{"key":"value"}`), // additional metadata
	})
	assert.NoError(t, err)

	events, _ := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	require.NoError(t, client.Start(context.Background()))
	select {
	case event := <-events:
		assert.Equal(t, river.EventKindJobCompleted, event.Kind)
	case <-ctx.Done():
		require.Fail(t, "did not receive event before timeout")
	}

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2, "wrong number of spans")
	assert.Equal(t, "river.send", spans[0].OperationName())
	assert.Equal(t, "river.process", spans[1].OperationName())

	meta := metadataToMap(t, jobMetadata)
	assert.Equal(t, strconv.FormatUint(spans[0].SpanID(), 10), meta["x-datadog-parent-id"])
	assert.Equal(t, strconv.FormatUint(spans[0].TraceID(), 10), meta["x-datadog-trace-id"])
}

func TestInvalidMetadata(t *testing.T) {
	ctx, mt, driver := setup(t)

	client, err := river.NewClient(driver, &river.Config{
		TestOnly:            true,
		MaxAttempts:         1,
		JobTimeout:          1 * time.Second,
		JobInsertMiddleware: []rivertype.JobInsertMiddleware{NewInsertMiddleware()},
	})
	require.NoError(t, err)
	t.Cleanup(stopClientF(t, client))

	_, err = client.Insert(ctx, jobArg{Data: "data"}, &river.InsertOpts{
		Metadata: []byte(`invalid json`),
	})
	assert.Error(t, err)

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1, "wrong number of spans")
	assert.Equal(t, "river.send", spans[0].OperationName())

	assert.Equal(t, err.Error(), spans[0].Tags()[ext.ErrorMsg])
}

func setup(t *testing.T) (context.Context, mocktracer.Tracer, *riverpgxv5.Driver) {
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	pool, err := newPool(ctx)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return ctx, mt, riverpgxv5.New(pool)
}

func stopClientF(t *testing.T, client *river.Client[pgx.Tx]) func() {
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		assert.NoError(t, client.StopAndCancel(ctx))
	}
}

type jobArg struct {
	Data string `json:"data"`
}

func (a jobArg) Kind() string {
	return "kind"
}

type testWorker struct {
	river.WorkerDefaults[jobArg]
	f func(ctx context.Context, job *river.Job[jobArg]) error
}

func (w testWorker) Work(ctx context.Context, job *river.Job[jobArg]) error {
	return w.f(ctx, job)
}
