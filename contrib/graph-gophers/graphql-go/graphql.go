package graphql

import (
	"context"
	"fmt"

	"github.com/graph-gophers/graphql-go/errors"
	"github.com/graph-gophers/graphql-go/introspection"
	"github.com/graph-gophers/graphql-go/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	graphqlFieldTag = "graphql.field"
	graphqlQueryTag = "graphql.query"
	graphqlTypeTag  = "graphql.type"
)

// A Tracer implements the graphql-go/trace.Tracer interface by sending traces
// to the Datadog tracer.
type Tracer struct {
	cfg *config
}

// TraceQuery traces a GraphQL query.
func (t *Tracer) TraceQuery(ctx context.Context, queryString string, operationName string, variables map[string]interface{}, varTypes map[string]*introspection.Type) (context.Context, trace.TraceQueryFinishFunc) {
	span, ctx := tracer.StartSpanFromContext(ctx, "graphql.request",
		tracer.ServiceName(t.cfg.serviceName),
		tracer.Tag(graphqlQueryTag, queryString),
	)

	return ctx, func(errs []*errors.QueryError) {
		var err error
		if len(errs) == 1 {
			err = errs[0]
		} else if len(errs) > 1 {
			msg := errs[0].Error() +
				fmt.Sprintf(" (and %d more errors)", len(errs)-1)
			err = errors.Errorf("%s", msg)
		}
		span.Finish(tracer.WithError(err))
	}
}

// TraceField traces a GraphQL field access.
func (t *Tracer) TraceField(ctx context.Context, label string, typeName string, fieldName string, trivial bool, args map[string]interface{}) (context.Context, trace.TraceFieldFinishFunc) {
	span, ctx := tracer.StartSpanFromContext(ctx, "graphql.field",
		tracer.ServiceName(t.cfg.serviceName),
		tracer.Tag(graphqlFieldTag, fieldName),
		tracer.Tag(graphqlTypeTag, typeName),
	)
	return ctx, func(err *errors.QueryError) {
		// this is necessary otherwise the span gets marked as an error
		if err != nil {
			span.Finish(tracer.WithError(err))
		} else {
			span.Finish()
		}
	}
}

// New creates a new Tracer.
func New(opts ...Option) trace.Tracer {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	return &Tracer{
		cfg: cfg,
	}
}
