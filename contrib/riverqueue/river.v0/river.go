package river

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const (
	componentName = "riverqueue/river.v0"
)

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/riverqueue/river")
}

type InsertMiddleware struct {
	river.JobInsertMiddlewareDefaults
	cfg *config
}

func NewInsertMiddleware(opts ...Option) *InsertMiddleware {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	cfg.spanOpts = append(cfg.spanOpts,
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindProducer),
		tracer.Tag(ext.MessagingSystem, "river"))
	if cfg.serviceName != "" {
		cfg.spanOpts = append(cfg.spanOpts, tracer.ServiceName(cfg.serviceName))
	}
	if cfg.measured {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Measured())
	}
	log.Debug("contrib/riverqueue/river.v0/river: Configuring Insert Middleware: %#v", cfg)
	return &InsertMiddleware{cfg: cfg}
}

func (m *InsertMiddleware) InsertMany(
	ctx context.Context,
	manyParams []*rivertype.JobInsertParams,
	doInner func(ctx context.Context) ([]*rivertype.JobInsertResult, error),
) (results []*rivertype.JobInsertResult, doInnerErr error) {
	opts := append(options.Copy(m.cfg.spanOpts...),
		tracer.ResourceName("river.insert_many"),
	)

	span, ctx := tracer.StartSpanFromContext(ctx, m.cfg.insertSpanName, opts...)
	defer func() {
		span.Finish(tracer.WithError(doInnerErr))
	}()
	spanCtx := span.Context()

	for _, params := range manyParams {
		tryInjectSpanContext(spanCtx, params)
	}

	return doInner(ctx)
}

type WorkerMiddleware struct {
	river.WorkerMiddlewareDefaults
	cfg *config
}

func NewWorkerMiddleware(opts ...Option) *WorkerMiddleware {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	cfg.spanOpts = append(cfg.spanOpts,
		tracer.SpanType(ext.SpanTypeMessageConsumer),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindConsumer),
		tracer.Tag(ext.MessagingSystem, "river"))
	if cfg.serviceName != "" {
		cfg.spanOpts = append(cfg.spanOpts, tracer.ServiceName(cfg.serviceName))
	}
	if cfg.measured {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Measured())
	}
	log.Debug("contrib/riverqueue/river.v0/river: Configuring Worker Middleware: %#v", cfg)
	return &WorkerMiddleware{cfg: cfg}
}

func (m *WorkerMiddleware) Work(ctx context.Context, job *rivertype.JobRow, doInner func(ctx context.Context) error) (doInnerErr error) {
	opts := append(options.Copy(m.cfg.spanOpts...),
		tracer.ResourceName(job.Kind),
		tracer.Tag("river_job.id", job.ID),
		tracer.Tag("river_job.scheduled_at", job.ScheduledAt),
		tracer.Tag("river_job.queue", job.Queue),
		tracer.Tag("river_job.kind", job.Kind),
		tracer.Tag("river_job.attempt", job.Attempt),
	)

	if carrier, err := metadataToCarrier(job.Metadata); err != nil {
		log.Debug("contrib/riverqueue/river.v0/river: Failed to parse job metadata: %v", err)
	} else {
		if parentSpanCtx, err := tracer.Extract(carrier); err == nil { // if NO error
			opts = append(opts, tracer.ChildOf(parentSpanCtx))
			if linksCtx, ok := parentSpanCtx.(ddtrace.SpanContextWithLinks); ok {
				if spanLinks := linksCtx.SpanLinks(); spanLinks != nil {
					opts = append(opts, tracer.WithSpanLinks(linksCtx.SpanLinks()))
				}
			}
		}
	}

	span, ctx := tracer.StartSpanFromContext(ctx, m.cfg.workSpanName, opts...)
	defer func() {
		span.Finish(tracer.WithError(doInnerErr))
	}()

	return doInner(ctx)
}

func tryInjectSpanContext(spanCtx ddtrace.SpanContext, params *rivertype.JobInsertParams) {
	carrier, err := metadataToCarrier(params.Metadata)
	if err != nil {
		log.Debug("contrib/riverqueue/river.v0/river: Failed to parse job metadata: %v", err)
		return
	}

	if err := tracer.Inject(spanCtx, carrier); err != nil {
		log.Debug("contrib/riverqueue/river.v0/river: Failed to inject span context into job metadata: %v", err)
		return
	}

	metadataWithCtx, err := json.Marshal(carrier)
	if err != nil {
		log.Debug("contrib/riverqueue/river.v0/river: Failed to marshal carrier: %v", err)
		return
	}
	params.Metadata = metadataWithCtx
}

func metadataToCarrier(metadata []byte) (jsonCarrier, error) {
	var carrier jsonCarrier
	if len(metadata) == 0 {
		return jsonCarrier{}, nil
	}
	if err := json.Unmarshal(metadata, &carrier); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %v", err)
	}
	return carrier, nil
}
