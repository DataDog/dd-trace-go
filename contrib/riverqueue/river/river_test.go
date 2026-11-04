// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package river

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func insertParams(kind, queue string, metadata []byte) *rivertype.JobInsertParams {
	return &rivertype.JobInsertParams{
		Kind:        kind,
		Queue:       queue,
		MaxAttempts: 25,
		Priority:    1,
		Metadata:    metadata,
		EncodedArgs: []byte(`{}`),
	}
}

func insertResults(manyParams []*rivertype.JobInsertParams) []*rivertype.JobInsertResult {
	results := make([]*rivertype.JobInsertResult, len(manyParams))
	for i, p := range manyParams {
		results[i] = &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: int64(i + 1), Kind: p.Kind, Queue: p.Queue, Metadata: p.Metadata}}
	}
	return results
}

func TestInsertMany(t *testing.T) {
	insertErr := errors.New("insert failed")

	tests := []struct {
		name       string
		opts       []Option
		params     []*rivertype.JobInsertParams
		innerErr   error
		skipped    []bool
		wantMeta   map[string]any
		wantErr    error
		wantSpanEr bool
		wantInject bool
	}{
		{
			name:   "single job",
			params: []*rivertype.JobInsertParams{insertParams("sort", "default", []byte(`{}`))},
			wantMeta: map[string]any{
				ext.ResourceName:             "sort",
				ext.MessagingDestinationName: "default",
				tagMessagingBatchCount:       1,
				tagUniqueSkippedCount:        0,
			},
			wantInject: true,
		},
		{
			name: "batch of one kind and queue",
			params: []*rivertype.JobInsertParams{
				insertParams("sort", "default", nil),
				insertParams("sort", "default", nil),
			},
			skipped: []bool{false, true},
			wantMeta: map[string]any{
				ext.ResourceName:             "sort",
				ext.MessagingDestinationName: "default",
				tagMessagingBatchCount:       2,
				tagUniqueSkippedCount:        1,
			},
			wantInject: true,
		},
		{
			name: "batch of several kinds and queues",
			params: []*rivertype.JobInsertParams{
				insertParams("sort", "default", nil),
				insertParams("email", "mail", nil),
			},
			wantMeta: map[string]any{
				ext.ResourceName:       resourceMultipleKinds,
				tagMessagingBatchCount: 2,
			},
			wantInject: true,
		},
		{
			name:     "empty batch",
			params:   []*rivertype.JobInsertParams{},
			wantMeta: map[string]any{ext.ResourceName: resourceNoJobsForInsert, tagMessagingBatchCount: 0},
		},
		{
			name:       "insert error",
			params:     []*rivertype.JobInsertParams{insertParams("sort", "default", nil)},
			innerErr:   insertErr,
			wantMeta:   map[string]any{ext.ResourceName: "sort"},
			wantErr:    insertErr,
			wantSpanEr: true,
			wantInject: true,
		},
		{
			name:       "insert error ignored by error check",
			opts:       []Option{WithErrorCheck(func(err error) bool { return !errors.Is(err, insertErr) })},
			params:     []*rivertype.JobInsertParams{insertParams("sort", "default", nil)},
			innerErr:   insertErr,
			wantMeta:   map[string]any{ext.ResourceName: "sort"},
			wantErr:    insertErr,
			wantInject: true,
		},
		{
			name:     "distributed tracing disabled",
			opts:     []Option{WithDistributedTracing(false)},
			params:   []*rivertype.JobInsertParams{insertParams("sort", "default", []byte(`{}`))},
			wantMeta: map[string]any{ext.ResourceName: "sort"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			root, ctx := tracer.StartSpanFromContext(context.Background(), "root")
			original := make([][]byte, len(tt.params))
			for i, p := range tt.params {
				original[i] = p.Metadata
			}

			var innerCtx context.Context
			m := NewMiddleware(tt.opts...)
			_, err := m.InsertMany(ctx, tt.params, func(ctx context.Context) ([]*rivertype.JobInsertResult, error) {
				innerCtx = ctx
				results := insertResults(tt.params)
				for i, skipped := range tt.skipped {
					results[i].UniqueSkippedAsDuplicate = skipped
				}
				return results, tt.innerErr
			})
			root.Finish()
			assert.Equal(t, tt.wantErr, err)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2)
			span := spans[0]
			assert.Equal(t, insertOperationName, span.OperationName())
			assert.Equal(t, root.Context().SpanID(), span.ParentID())
			assert.Equal(t, ext.SpanKindProducer, span.Tag(ext.SpanKind))
			assert.Equal(t, ext.SpanTypeMessageProducer, span.Tag(ext.SpanType))
			assert.Equal(t, string(instrumentation.PackageRiverqueueRiver), span.Tag(ext.Component))
			assert.Equal(t, "river", span.Tag(ext.MessagingSystem))
			assert.Equal(t, string(instrumentation.PackageRiverqueueRiver), span.Integration())
			for k, v := range tt.wantMeta {
				assert.EqualValues(t, v, span.Tag(k), "tag %s", k)
			}
			if tt.wantSpanEr {
				assert.Equal(t, tt.innerErr.Error(), span.Tag(ext.ErrorMsg))
			} else {
				assert.Nil(t, span.Tag(ext.ErrorMsg))
			}

			// The inner call gets the insert span's context.
			active, ok := tracer.SpanFromContext(innerCtx)
			require.True(t, ok)
			assert.Equal(t, span.SpanID(), active.Context().SpanID())

			for i, p := range tt.params {
				if !tt.wantInject {
					assert.Equal(t, original[i], p.Metadata)
					continue
				}
				sctx := extractFromMetadata(p.Metadata)
				require.NotNil(t, sctx, "metadata %s", p.Metadata)
				assert.Equal(t, span.SpanID(), sctx.SpanID())
				assert.Equal(t, span.Context().TraceID(), sctx.TraceID())
			}
		})
	}
}

func TestInsertManyKeepsMetadata(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	params := []*rivertype.JobInsertParams{
		insertParams("sort", "default", []byte(`{"customer":"acme","nested":{"a":[1,2]}}`)),
		insertParams("sort", "default", []byte(`not json`)),
	}
	m := NewMiddleware()
	_, err := m.InsertMany(context.Background(), params, func(context.Context) ([]*rivertype.JobInsertResult, error) {
		return insertResults(params), nil
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(params[0].Metadata, &got))
	assert.Equal(t, "acme", got["customer"])
	assert.Equal(t, map[string]any{"a": []any{1.0, 2.0}}, got["nested"])
	assert.Contains(t, got, metadataKey)

	// Metadata that isn't a JSON object is left for River to reject, and the
	// insert itself is still traced.
	assert.Equal(t, []byte(`not json`), params[1].Metadata)
	require.Len(t, mt.FinishedSpans(), 1)
	assert.Nil(t, mt.FinishedSpans()[0].Tag(ext.ErrorMsg))
}

func TestInsertManyPanic(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	m := NewMiddleware()
	params := []*rivertype.JobInsertParams{insertParams("sort", "default", nil)}
	assert.PanicsWithValue(t, "boom", func() {
		_, _ = m.InsertMany(context.Background(), params, func(context.Context) ([]*rivertype.JobInsertResult, error) {
			panic("boom")
		})
	})
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "panic: boom", spans[0].Tag(ext.ErrorMsg))
}

func jobRow(metadata []byte) *rivertype.JobRow {
	return &rivertype.JobRow{
		ID:          42,
		Attempt:     1,
		MaxAttempts: 25,
		Kind:        "sort",
		Queue:       "default",
		Priority:    2,
		Metadata:    metadata,
	}
}

func TestWork(t *testing.T) {
	workErr := errors.New("work failed")

	tests := []struct {
		name          string
		opts          []Option
		innerErr      error
		wantErrMsg    string
		wantCancelled bool
		wantSnoozed   bool
	}{
		{name: "success"},
		{name: "error", innerErr: workErr, wantErrMsg: "work failed"},
		{name: "wrapped error", innerErr: errors.Join(errors.New("context"), workErr), wantErrMsg: "context\nwork failed"},
		{name: "snooze is not an error", innerErr: &rivertype.JobSnoozeError{Duration: time.Minute}, wantSnoozed: true},
		{name: "cancel is an error", innerErr: rivertype.JobCancel(workErr), wantErrMsg: "JobCancelError: work failed", wantCancelled: true},
		{
			name:     "error ignored by error check",
			opts:     []Option{WithErrorCheck(func(err error) bool { return !errors.Is(err, workErr) })},
			innerErr: workErr,
		},
		{
			name:        "error check does not apply to snoozes",
			opts:        []Option{WithErrorCheck(func(error) bool { return true })},
			innerErr:    &rivertype.JobSnoozeError{Duration: time.Minute},
			wantSnoozed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			var innerCtx context.Context
			m := NewMiddleware(tt.opts...)
			err := m.Work(context.Background(), jobRow([]byte(`{}`)), func(ctx context.Context) error {
				innerCtx = ctx
				return tt.innerErr
			})
			assert.Equal(t, tt.innerErr, err)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			span := spans[0]
			assert.Equal(t, workOperationName, span.OperationName())
			assert.Equal(t, "sort", span.Tag(ext.ResourceName))
			assert.Equal(t, ext.SpanKindConsumer, span.Tag(ext.SpanKind))
			assert.Equal(t, ext.SpanTypeMessageConsumer, span.Tag(ext.SpanType))
			assert.Equal(t, string(instrumentation.PackageRiverqueueRiver), span.Tag(ext.Component))
			assert.Equal(t, "river", span.Tag(ext.MessagingSystem))
			assert.Equal(t, "default", span.Tag(ext.MessagingDestinationName))
			assert.EqualValues(t, 42, span.Tag(ext.MessagingMessageID))
			assert.Equal(t, "sort", span.Tag(tagJobKind))
			assert.EqualValues(t, 1, span.Tag(tagJobAttempt))
			assert.EqualValues(t, 25, span.Tag(tagJobMaxAttempts))
			assert.EqualValues(t, 2, span.Tag(tagJobPriority))
			assert.EqualValues(t, uint64(0), span.ParentID())

			if tt.wantErrMsg != "" {
				assert.Equal(t, tt.wantErrMsg, span.Tag(ext.ErrorMsg))
			} else {
				assert.Nil(t, span.Tag(ext.ErrorMsg))
			}
			if tt.wantCancelled {
				assert.Equal(t, "true", span.Tag(tagJobCancelled))
			} else {
				assert.Nil(t, span.Tag(tagJobCancelled))
			}
			if tt.wantSnoozed {
				assert.Equal(t, "true", span.Tag(tagJobSnoozed))
			} else {
				assert.Nil(t, span.Tag(tagJobSnoozed))
			}

			active, ok := tracer.SpanFromContext(innerCtx)
			require.True(t, ok)
			assert.Equal(t, span.SpanID(), active.Context().SpanID())
		})
	}
}

func TestWorkParent(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// The job was inserted under insertSpan, and is worked under ctxSpan.
	insertSpan := tracer.StartSpan("insert")
	carrier := make(tracer.TextMapCarrier)
	require.NoError(t, tracer.Inject(insertSpan.Context(), carrier))
	encoded, err := json.Marshal(carrier)
	require.NoError(t, err)
	propagated, err := withTraceContext(nil, encoded)
	require.NoError(t, err)
	insertSpan.Finish()

	ctxSpan, ctx := tracer.StartSpanFromContext(context.Background(), "poller")
	defer ctxSpan.Finish()

	tests := []struct {
		name       string
		opts       []Option
		ctx        context.Context
		metadata   []byte
		wantParent *tracer.Span
	}{
		{name: "propagated context wins over the span in ctx", metadata: propagated, wantParent: insertSpan},
		{name: "propagated context without a span in ctx", ctx: context.Background(), metadata: propagated, wantParent: insertSpan},
		{name: "no propagated context", metadata: []byte(`{"other":"value"}`), wantParent: ctxSpan},
		{name: "nil metadata", metadata: nil, wantParent: ctxSpan},
		{name: "invalid propagated context", metadata: []byte(`{"_datadog":{"x-datadog-trace-id":1}}`), wantParent: ctxSpan},
		{name: "empty propagated context", metadata: []byte(`{"_datadog":{}}`), wantParent: ctxSpan},
		{name: "unparsable metadata", metadata: []byte(`{"_datadog":`), wantParent: ctxSpan},
		{name: "propagation disabled", opts: []Option{WithDistributedTracing(false)}, metadata: propagated, wantParent: ctxSpan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt.Reset()
			m := NewMiddleware(tt.opts...)
			workCtx := ctx
			if tt.ctx != nil {
				workCtx = tt.ctx
			}
			var innerCtx context.Context
			require.NoError(t, m.Work(workCtx, jobRow(tt.metadata), func(ctx context.Context) error {
				innerCtx = ctx
				return nil
			}))
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			assert.Equal(t, tt.wantParent.Context().SpanID(), spans[0].ParentID())
			assert.Equal(t, tt.wantParent.Context().TraceID(), spans[0].Context().TraceID())
			active, ok := tracer.SpanFromContext(innerCtx)
			require.True(t, ok)
			assert.Equal(t, spans[0].SpanID(), active.Context().SpanID())
		})
	}
}

func TestWorkPanic(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		wantMsg string
	}{
		{name: "string", value: "boom", wantMsg: "panic: boom"},
		{name: "error", value: errors.New("bad state"), wantMsg: "panic: bad state"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			m := NewMiddleware()
			assert.PanicsWithValue(t, tt.value, func() {
				_ = m.Work(context.Background(), jobRow(nil), func(context.Context) error { panic(tt.value) })
			})
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			assert.Equal(t, tt.wantMsg, spans[0].Tag(ext.ErrorMsg))
		})
	}
}

func TestWorkGoexit(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	m := NewMiddleware()
	var wg sync.WaitGroup
	wg.Go(func() {
		_ = m.Work(context.Background(), jobRow(nil), func(context.Context) error {
			// A worker that calls t.FailNow (as rivertest workers can) exits its
			// goroutine this way: the span must finish without a fake panic.
			runtime.Goexit()
			return nil
		})
	})
	wg.Wait()
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Nil(t, spans[0].Tag(ext.ErrorMsg))
}

func TestOptions(t *testing.T) {
	t.Run("default service", func(t *testing.T) {
		testutils.SetGlobalServiceName(t, "my-app")
		mt := mocktracer.Start()
		defer mt.Stop()

		require.NoError(t, NewMiddleware().Work(context.Background(), jobRow(nil), func(context.Context) error { return nil }))
		span := mt.FinishedSpans()[0]
		assert.Equal(t, "my-app", span.Tag(ext.ServiceName))
		// No service source is reported for the global service.
		assert.Nil(t, span.Tag(ext.KeyServiceSource))
	})
	t.Run("no service", func(t *testing.T) {
		testutils.SetGlobalServiceName(t, "")
		mt := mocktracer.Start()
		defer mt.Stop()

		require.NoError(t, NewMiddleware().Work(context.Background(), jobRow(nil), func(context.Context) error { return nil }))
		assert.Nil(t, mt.FinishedSpans()[0].Tag(ext.KeyServiceSource))
	})
	t.Run("WithService", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		m := NewMiddleware(WithService("jobs"))
		require.NoError(t, m.Work(context.Background(), jobRow(nil), func(context.Context) error { return nil }))
		_, err := m.InsertMany(context.Background(), nil, func(context.Context) ([]*rivertype.JobInsertResult, error) { return nil, nil })
		require.NoError(t, err)
		for _, span := range mt.FinishedSpans() {
			assert.Equal(t, "jobs", span.Tag(ext.ServiceName))
			assert.Equal(t, instrumentation.ServiceSourceWithServiceOption, span.Tag(ext.KeyServiceSource))
		}
	})
	t.Run("WithCustomTag", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		m := NewMiddleware(WithCustomTag("team", "payments"), WithCustomTag(ext.MessagingDestinationName, "renamed"), nil)
		require.NoError(t, m.Work(context.Background(), jobRow(nil), func(context.Context) error { return nil }))
		require.NoError(t, m.Work(context.Background(), jobRow(nil), func(context.Context) error { return nil }))
		spans := mt.FinishedSpans()
		require.Len(t, spans, 2)
		for _, span := range spans {
			assert.Equal(t, "payments", span.Tag("team"))
			// Custom tags win over the integration's own tags.
			assert.Equal(t, "renamed", span.Tag(ext.MessagingDestinationName))
			assert.Equal(t, "sort", span.Tag(ext.ResourceName))
		}
	})
}

func TestTracerNotStarted(t *testing.T) {
	// Without a running tracer, jobs are inserted and worked as usual, and their
	// metadata is left alone.
	m := NewMiddleware()
	params := []*rivertype.JobInsertParams{insertParams("sort", "default", []byte(`{}`))}
	_, err := m.InsertMany(context.Background(), params, func(context.Context) ([]*rivertype.JobInsertResult, error) {
		return insertResults(params), nil
	})
	require.NoError(t, err)
	assert.Equal(t, []byte(`{}`), params[0].Metadata)
	require.NoError(t, m.Work(context.Background(), jobRow(params[0].Metadata), func(context.Context) error { return nil }))
}

func TestIsMiddleware(t *testing.T) {
	m := NewMiddleware()
	assert.True(t, m.IsMiddleware())
	assert.True(t, m.IsPlugin())
}

func TestAddMiddleware(t *testing.T) {
	other := &otherMiddleware{}

	t.Run("Middleware", func(t *testing.T) {
		mws := make([]rivertype.Middleware, 1, 4) // spare capacity must not be written to
		mws[0] = other
		got := AddMiddleware(mws)
		require.Len(t, got, 2)
		assert.IsType(t, &Middleware{}, got[0])
		assert.Same(t, other, got[1])
		assert.Equal(t, []rivertype.Middleware{other}, mws)
		assert.Nil(t, mws[:2][1])

		// A slice that already has the middleware is returned as is.
		again := AddMiddleware(got)
		assert.Equal(t, got, again)
	})
	t.Run("nil slice", func(t *testing.T) {
		got := AddMiddleware[rivertype.Middleware](nil, WithService("svc"))
		require.Len(t, got, 1)
		assert.Equal(t, "svc", got[0].(*Middleware).cfg.service.Load().name)
	})
	t.Run("deprecated JobInsertMiddleware", func(t *testing.T) {
		got := AddMiddleware([]rivertype.JobInsertMiddleware{})
		require.Len(t, got, 1)
		assert.IsType(t, &Middleware{}, got[0])
	})
	t.Run("deprecated WorkerMiddleware", func(t *testing.T) {
		got := AddMiddleware([]rivertype.WorkerMiddleware{other})
		require.Len(t, got, 2)
		assert.IsType(t, &Middleware{}, got[0])
	})
	t.Run("type the middleware does not implement", func(t *testing.T) {
		mws := []notImplemented{&otherMiddleware{}}
		assert.Equal(t, mws, AddMiddleware(mws))
	})
}

type otherMiddleware struct{}

func (*otherMiddleware) IsMiddleware() bool { return true }

func (*otherMiddleware) Work(ctx context.Context, _ *rivertype.JobRow, doInner func(context.Context) error) error {
	return doInner(ctx)
}

func (*otherMiddleware) Other() {}

// notImplemented is a middleware interface that Middleware does not implement.
type notImplemented interface {
	rivertype.Middleware
	Other()
}
