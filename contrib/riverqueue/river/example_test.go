// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package river_test

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	rivertrace "github.com/DataDog/dd-trace-go/contrib/riverqueue/river/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type SortArgs struct {
	Strings []string `json:"strings"`
}

func (SortArgs) Kind() string { return "sort" }

type SortWorker struct {
	river.WorkerDefaults[SortArgs]
}

func (w *SortWorker) Work(ctx context.Context, job *river.Job[SortArgs]) error {
	// ctx carries the "river.work" span, which continues the trace of the
	// request that inserted the job.
	span, _ := tracer.StartSpanFromContext(ctx, "sort")
	defer span.Finish()
	return nil
}

func Example() {
	tracer.Start()
	defer tracer.Stop()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, "postgres://localhost:5432/river")
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	workers := river.NewWorkers()
	river.AddWorker(workers, &SortWorker{})

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		// Put the tracing middleware first, so that it runs outermost.
		Middleware: []rivertype.Middleware{rivertrace.NewMiddleware()},
		Queues:     map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 100}},
		Workers:    workers,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := client.Start(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Stop(ctx)

	// The insert is traced as a child of the span in ctx, if any.
	if _, err := client.Insert(ctx, SortArgs{Strings: []string{"whale", "tiger"}}, nil); err != nil {
		log.Fatal(err)
	}
}

func ExampleNewMiddleware_withOptions() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, "postgres://localhost:5432/river")
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	middleware := rivertrace.NewMiddleware(
		rivertrace.WithService("billing-jobs"),
		rivertrace.WithCustomTag("team", "billing"),
		// Don't store trace context in job metadata. Work spans then start new
		// traces instead of continuing the trace that inserted the job.
		rivertrace.WithDistributedTracing(false),
	)
	// An insert-only client: it inserts jobs, other processes work them.
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Middleware: []rivertype.Middleware{middleware},
	})
	if err != nil {
		log.Fatal(err)
	}
	if _, err := client.Insert(ctx, SortArgs{Strings: []string{"b", "a"}}, nil); err != nil {
		log.Fatal(err)
	}
}
