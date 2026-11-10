// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package river provides tracing for River (https://github.com/riverqueue/river),
// a Postgres-backed job queue for Go.
//
// NewMiddleware returns a River middleware that creates a "river.insert_many"
// producer span around every job insert (Insert, InsertTx, InsertMany, ...) and a
// "river.work" consumer span around every job attempt:
//
//	workers := river.NewWorkers()
//	river.AddWorker(workers, &SortWorker{})
//
//	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
//		Middleware: []rivertype.Middleware{rivertrace.NewMiddleware()},
//		Queues:     map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 100}},
//		Workers:    workers,
//	})
//
// The trace context of the insert span, including its sampling decision, is
// stored in each job's metadata under the "_datadog" key, so the spans that work
// a job are children of the span that inserted it, even when the job runs in
// another process or is retried hours later. WithDistributedTracing(false)
// turns this off.
//
// Snoozed jobs (river.JobSnooze) are not errors. Cancelled jobs (river.JobCancel)
// are errors and are tagged with river.job.cancelled. Panics are recorded on the
// span and re-raised with their original value, so River still records them.
//
// Under Orchestrion, the middleware is added to every river.Client automatically.
package river // import "github.com/DataDog/dd-trace-go/contrib/riverqueue/river/v2"

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/riverqueue/river/rivertype"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const (
	componentName = instrumentation.PackageRiverqueueRiver

	// messagingSystem is the value of the messaging.system tag.
	messagingSystem = "river"

	insertOperationName = "river.insert_many"
	workOperationName   = "river.work"

	// Tags specific to River.
	tagJobKind              = "river.job.kind"
	tagJobAttempt           = "river.job.attempt"
	tagJobMaxAttempts       = "river.job.max_attempts"
	tagJobPriority          = "river.job.priority"
	tagJobCancelled         = "river.job.cancelled"
	tagJobSnoozed           = "river.job.snoozed"
	tagUniqueSkippedCount   = "river.insert.unique_skipped_as_duplicate_count"
	tagMessagingBatchCount  = "messaging.batch.message_count"
	resourceMultipleKinds   = "multiple"
	resourceNoJobsForInsert = "none"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(componentName)
}

// Middleware is a River middleware that traces job inserts and job work. It
// implements rivertype.JobInsertMiddleware and rivertype.WorkerMiddleware, and
// is meant to be added to river.Config.Middleware.
type Middleware struct {
	cfg *config

	// Tags that are the same on every span, built once.
	insertBase *tracer.StartSpanConfig
	workBase   *tracer.StartSpanConfig
	// customTags applies WithCustomTag values after the cached base, so they
	// win over the integration's own tags on key collision. Nil if unset.
	customTags tracer.StartSpanOption
}

var (
	_ rivertype.JobInsertMiddleware = (*Middleware)(nil)
	_ rivertype.WorkerMiddleware    = (*Middleware)(nil)
)

// NewMiddleware returns a River middleware that traces job inserts and job work.
// Add it to river.Config.Middleware. Middleware listed first runs outermost, so
// put it first to include the time spent in other middleware.
func NewMiddleware(opts ...Option) *Middleware {
	cfg := newConfig(opts...)
	instr.Logger().Debug("contrib/riverqueue/river: configuring middleware: %#v", cfg)
	m := &Middleware{
		cfg:        cfg,
		insertBase: newSpanBase(ext.SpanKindProducer, ext.SpanTypeMessageProducer),
		workBase:   newSpanBase(ext.SpanKindConsumer, ext.SpanTypeMessageConsumer),
	}
	if len(cfg.tags) > 0 {
		m.customTags = tracer.WithTags(cfg.tags)
	}
	return m
}

func newSpanBase(spanKind, spanType string) *tracer.StartSpanConfig {
	return tracer.NewStartSpanConfig(
		tracer.SpanType(spanType),
		tracer.Tag(ext.SpanKind, spanKind),
		tracer.Tag(ext.Component, string(componentName)),
		tracer.Tag(ext.MessagingSystem, messagingSystem),
		tracer.Measured(),
	)
}

// startOptions returns the span start options shared by both span types. tags
// must be set first: see the ordering rule in contrib/INTEGRATIONS.md.
func (m *Middleware) startOptions(tags map[string]any, base *tracer.StartSpanConfig, extra int) []tracer.StartSpanOption {
	opts := make([]tracer.StartSpanOption, 0, 4+extra)
	opts = append(opts,
		tracer.WithTags(tags),
		tracer.WithStartSpanConfig(base),
		m.cfg.resolveService().opt,
	)
	if m.customTags != nil {
		opts = append(opts, m.customTags)
	}
	return opts
}

// IsMiddleware implements rivertype.Middleware.
func (*Middleware) IsMiddleware() bool { return true }

// IsPlugin implements rivertype.Plugin.
func (*Middleware) IsPlugin() bool { return true }

// InsertMany implements rivertype.JobInsertMiddleware. It traces a batch insert
// with one span and, unless distributed tracing is disabled, stores that span's
// context in the metadata of every job in the batch.
func (m *Middleware) InsertMany(ctx context.Context, manyParams []*rivertype.JobInsertParams, doInner func(context.Context) ([]*rivertype.JobInsertResult, error)) (results []*rivertype.JobInsertResult, err error) {
	tags := map[string]any{
		ext.ResourceName:       insertResource(manyParams),
		tagMessagingBatchCount: len(manyParams),
	}
	if queue, ok := commonQueue(manyParams); ok {
		tags[ext.MessagingDestinationName] = queue
	}
	span, ctx := tracer.StartSpanFromContext(ctx, insertOperationName, m.startOptions(tags, m.insertBase, 0)...)

	panicked := true
	defer func() {
		if panicked {
			finishPanicked(span, recover())
			return
		}
		var skipped int
		for _, r := range results {
			if r != nil && r.UniqueSkippedAsDuplicate {
				skipped++
			}
		}
		span.SetTag(tagUniqueSkippedCount, skipped)
		span.Finish(tracer.WithError(m.spanError(err)))
	}()

	if m.cfg.distributedTracing {
		if injectErr := injectIntoParams(span, manyParams); injectErr != nil {
			instr.Logger().Debug("contrib/riverqueue/river: failed to inject trace context into job metadata: %s", injectErr.Error())
		}
	}

	results, err = doInner(ctx)
	panicked = false
	return results, err
}

// Work implements rivertype.WorkerMiddleware. It traces one attempt at working a
// job. The span is a child of the span that inserted the job when its metadata
// carries a trace context, and of the span in ctx otherwise.
func (m *Middleware) Work(ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error) (err error) {
	tags := map[string]any{
		ext.ResourceName:             job.Kind,
		ext.MessagingDestinationName: job.Queue,
		ext.MessagingMessageID:       strconv.FormatInt(job.ID, 10),
		tagJobKind:                   job.Kind,
		tagJobAttempt:                job.Attempt,
		tagJobMaxAttempts:            job.MaxAttempts,
		tagJobPriority:               job.Priority,
	}
	opts := m.startOptions(tags, m.workBase, 3)
	var parent *tracer.SpanContext
	if m.cfg.distributedTracing {
		parent = extractFromMetadata(job.Metadata)
	}
	var span *tracer.Span
	if parent == nil {
		span, ctx = tracer.StartSpanFromContext(ctx, workOperationName, opts...)
	} else {
		if links := parent.SpanLinks(); links != nil {
			opts = append(opts, tracer.WithSpanLinks(links))
		}
		opts = append(opts, tracer.ChildOf(parent))
		if _, ok := tracer.SpanFromContext(ctx); ok {
			// StartSpanFromContext would parent the span to the span in ctx, for
			// example one active when client.Start was called, over the span
			// that inserted the job. The inserting span is the one that matters.
			span = tracer.StartSpan(workOperationName, append(opts, withSpanContext(ctx))...)
			ctx = tracer.ContextWithSpan(ctx, span)
		} else {
			span, ctx = tracer.StartSpanFromContext(ctx, workOperationName, opts...)
		}
	}

	panicked := true
	defer func() {
		if panicked {
			finishPanicked(span, recover())
			return
		}
		var (
			cancelErr *rivertype.JobCancelError
			snoozeErr *rivertype.JobSnoozeError
		)
		switch {
		case errors.As(err, &snoozeErr):
			// A snooze is flow control, not a failure: River works the job
			// again later without counting an attempt.
			span.SetTag(tagJobSnoozed, true)
			span.Finish()
			return
		case errors.As(err, &cancelErr):
			span.SetTag(tagJobCancelled, true)
		}
		span.Finish(tracer.WithError(m.spanError(err)))
	}()

	err = doInner(ctx)
	panicked = false
	return err
}

// withSpanContext passes ctx to the tracer for the span being started, as
// StartSpanFromContext does, without making the span in ctx its parent.
func withSpanContext(ctx context.Context) tracer.StartSpanOption {
	return func(cfg *tracer.StartSpanConfig) {
		cfg.Context = ctx
	}
}

// finishPanicked records a panic on span, finishes it, and re-raises the panic
// with its original value so that River's own recovery still sees it. A nil
// recovered value means the goroutine is exiting through runtime.Goexit, which
// must not be turned into a panic.
func finishPanicked(span *tracer.Span, recovered any) {
	if recovered == nil {
		span.Finish()
		return
	}
	var err error
	switch v := recovered.(type) {
	case error:
		err = fmt.Errorf("panic: %w", v)
	default:
		err = fmt.Errorf("panic: %v", v)
	}
	span.Finish(tracer.WithError(err))
	panic(recovered)
}

// spanError returns err if it should be reported on the span, or nil.
func (m *Middleware) spanError(err error) error {
	if err == nil {
		return nil
	}
	if m.cfg.errCheck != nil && !m.cfg.errCheck(err) {
		return nil
	}
	return err
}

// insertResource returns the job kind of a batch, "multiple" when the batch
// holds more than one kind, and "none" for an empty batch.
func insertResource(manyParams []*rivertype.JobInsertParams) string {
	if len(manyParams) == 0 {
		return resourceNoJobsForInsert
	}
	kind := manyParams[0].Kind
	if !slices.ContainsFunc(manyParams[1:], func(p *rivertype.JobInsertParams) bool { return p.Kind != kind }) {
		return kind
	}
	return resourceMultipleKinds
}

// commonQueue returns the queue shared by every job of a batch.
func commonQueue(manyParams []*rivertype.JobInsertParams) (string, bool) {
	if len(manyParams) == 0 {
		return "", false
	}
	queue := manyParams[0].Queue
	for _, p := range manyParams[1:] {
		if p.Queue != queue {
			return "", false
		}
	}
	return queue, true
}
