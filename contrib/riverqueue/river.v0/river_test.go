package river

import (
	"context"
	"fmt"
	"log"
	"os"
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

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
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

func TestPropagation(t *testing.T) {
	ctx, mt, driver := setup(t)

	var (
		called         = false
		spanID         uint64
		jobID          int64
		jobScheduledAt time.Time
		jobMetadata    string
	)
	worker := testWorker{f: func(ctx context.Context, job *river.Job[jobArg]) error {
		assert.False(t, called, "work called twice")
		assert.Equal(t, "data", job.Args.Data)
		span, ok := tracer.SpanFromContext(ctx)
		assert.True(t, ok, "no span")
		assert.Equal(t, uint64(42), span.Context().TraceID(), "wrong trace id")
		spanID = span.Context().SpanID()
		jobID = job.ID
		jobScheduledAt = job.ScheduledAt
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
	assert.NoError(t, client.Start(context.Background()))
	select {
	case event := <-events:
		assert.Equal(t, river.EventKindJobCompleted, event.Kind)
		assert.True(t, called, "work not called")
	case <-ctx.Done():
		require.Fail(t, "did not receive event before timeout")
	}

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 3, "wrong number of spans")
	assert.Equal(t, "river.insert", spans[0].OperationName())
	assert.Equal(t, "propagation-test", spans[1].OperationName())
	assert.Equal(t, "river.work", spans[2].OperationName())

	assert.Equal(t, spans[1].SpanID(), spans[0].ParentID())
	assert.Equal(t, uint64(42), spans[0].TraceID())
	assert.Equal(t, map[string]interface{}{
		ext.SpanType:        ext.SpanTypeMessageProducer,
		ext.Component:       "riverqueue/river.v0",
		ext.SpanKind:        ext.SpanKindProducer,
		ext.MessagingSystem: "river",
		ext.ServiceName:     nil,
		ext.ResourceName:    "river.insert_many",
	}, spans[0].Tags())

	assert.Equal(t, spans[0].SpanID(), spans[2].ParentID())
	assert.Equal(t, uint64(42), spans[2].TraceID())
	assert.Equal(t, spanID, spans[2].SpanID())
	assert.Equal(t, map[string]interface{}{
		ext.SpanType:             ext.SpanTypeMessageConsumer,
		ext.Component:            "riverqueue/river.v0",
		ext.SpanKind:             ext.SpanKindConsumer,
		ext.MessagingSystem:      "river",
		ext.ResourceName:         "kind",
		"river_job.id":           jobID,
		"river_job.scheduled_at": jobScheduledAt,
		"river_job.queue":        "default",
		"river_job.kind":         "kind",
		"river_job.attempt":      1,
	}, spans[2].Tags())

	assert.JSONEq(t,
		fmt.Sprintf(`{"x-datadog-parent-id":"%d", "x-datadog-trace-id":"%d"}`,
			spans[0].SpanID(), spans[0].TraceID()),
		jobMetadata)
}

func TestPropagationWithServiceName(t *testing.T) {
	ctx, mt, driver := setup(t)

	worker := testWorker{f: func(ctx context.Context, job *river.Job[jobArg]) error { return nil }}
	workers := river.NewWorkers()
	require.NoError(t, river.AddWorkerSafely(workers, worker))

	client, err := river.NewClient(driver, &river.Config{
		TestOnly:            true,
		MaxAttempts:         1,
		JobTimeout:          1 * time.Second,
		JobInsertMiddleware: []rivertype.JobInsertMiddleware{NewInsertMiddleware(WithServiceName("insert.service"))},
		Workers:             workers,
		WorkerMiddleware:    []rivertype.WorkerMiddleware{NewWorkerMiddleware(WithServiceName("worker.service"))},
		Queues:              map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	require.NoError(t, err)
	t.Cleanup(stopClientF(t, client))

	span, insertCtx := tracer.StartSpanFromContext(ctx, "service-name-test", tracer.WithSpanID(42))
	_, err = client.Insert(insertCtx, jobArg{Data: "data"}, &river.InsertOpts{})
	assert.NoError(t, err)
	span.Finish()

	events, _ := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	assert.NoError(t, client.Start(context.Background()))
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
