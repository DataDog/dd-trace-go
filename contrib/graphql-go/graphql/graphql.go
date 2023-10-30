// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/graph-go/graphql"

import (
	"context"
	"fmt"
	"math"
	"reflect"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/graphqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/gqlerrors"
	"github.com/graphql-go/graphql/language/ast"
)

const componentName = "graphql-go/graphql"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/graphql-go/graphql")
}

const (
	tagGraphqlField         = "graphql.field"
	tagGraphqlQuery         = "graphql.query"
	tagGraphqlType          = "graphql.type"
	tagGraphqlOperationName = "graphql.operation.name"
)

func NewSchema(config graphql.SchemaConfig, options ...Option) (graphql.Schema, error) {
	extension := datadogExtension{}
	defaults(&extension.config)
	for _, opt := range options {
		opt(&extension.config)
	}

	config.Extensions = append(config.Extensions, extension)
	decorated := map[*graphql.FieldResolveFn]struct{}{}
	for _, field := range config.Query.Fields() {
		if _, found := decorated[&field.Resolve]; found {
			// Resolver was re-used for several fields...
			continue
		}
		resolver := field.Resolve
		field.Resolve = func(p graphql.ResolveParams) (data interface{}, err error) {
			var blocked bool
			ctx, op := graphqlsec.StartResolverOperation(p.Context, dyngo.NewDataListener(func(a *sharedsec.Action) {
				blocked = a.Blocking()
			}))
			defer func() {
				events := op.Finish(graphqlsec.OperationResult{Data: data, Error: err})
				if blocked {
					op.AddTag(instrumentation.BlockedRequestTag, true)
				}
				span, _ := tracer.SpanFromContext(p.Context)
				instrumentation.SetTags(span, op.Tags())
				if len(events) > 0 {
					// TODO(romain.marcadier): set security event tags
				}
			}()
			p.Context = ctx
			data, err = resolver(p)
			return
		}
		decorated[&field.Resolve] = struct{}{}
	}
	return graphql.NewSchema(config)
}

type datadogExtension struct{ config }

type contextKey struct{}
type contextData struct {
	query         string
	operationName string
	variables     map[string]any
}

var extensionName = reflect.TypeOf((*datadogExtension)(nil)).Elem().Name()

// Init is used to help you initialize the extension
func (i datadogExtension) Init(ctx context.Context, params *graphql.Params) context.Context {
	if ctx == nil {
		// In case we didn't get a user context, use a stand-in context.TODO
		ctx = context.TODO()
	}
	return context.WithValue(ctx, contextKey{}, contextData{query: params.RequestString, operationName: params.OperationName, variables: params.VariableValues})
}

// Name returns the name of the extension (make sure it's custom)
func (i datadogExtension) Name() string {
	return extensionName
}

// ParseDidStart is being called before starting the parse
func (i datadogExtension) ParseDidStart(ctx context.Context) (context.Context, graphql.ParseFinishFunc) {
	data, _ := ctx.Value(contextKey{}).(contextData)
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(i.config.serviceName),
		tracer.Tag(tagGraphqlQuery, data.query),
		tracer.Tag(ext.Component, componentName),
		tracer.Measured(),
	}
	if data.operationName != "" {
		opts = append(opts, tracer.Tag(tagGraphqlOperationName, data.operationName))
	}
	if !math.IsNaN(i.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, i.config.analyticsRate))
	}
	span, ctx := tracer.StartSpanFromContext(ctx, "graphql.parse", opts...)

	return ctx, func(err error) {
		span.Finish(tracer.WithError(err))
	}
}

// ValidationDidStart is called just before the validation begins
func (i datadogExtension) ValidationDidStart(ctx context.Context) (context.Context, graphql.ValidationFinishFunc) {
	data, _ := ctx.Value(contextKey{}).(contextData)
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(i.config.serviceName),
		tracer.Tag(tagGraphqlQuery, data.query),
		tracer.Tag(ext.Component, componentName),
		tracer.Measured(),
	}
	if data.operationName != "" {
		opts = append(opts, tracer.Tag(tagGraphqlOperationName, data.operationName))
	}
	if !math.IsNaN(i.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, i.config.analyticsRate))
	}
	span, ctx := tracer.StartSpanFromContext(ctx, "graphql.validation", opts...)

	return ctx, func(errs []gqlerrors.FormattedError) {
		span.Finish(tracer.WithError(toError(errs)))
	}
}

// ExecutionDidStart notifies about the start of the execution
func (i datadogExtension) ExecutionDidStart(ctx context.Context) (context.Context, graphql.ExecutionFinishFunc) {
	data, _ := ctx.Value(contextKey{}).(contextData)
	ctx, op := graphqlsec.StartQuery(ctx, graphqlsec.QueryArguments{
		Query:         data.query,
		OperationName: data.operationName,
		Variables:     data.variables,
	})

	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(i.config.serviceName),
		tracer.Tag(tagGraphqlQuery, data.query),
		tracer.Tag(ext.Component, componentName),
		tracer.Measured(),
	}
	if data.operationName != "" {
		opts = append(opts, tracer.Tag(tagGraphqlOperationName, data.operationName))
	}
	if !math.IsNaN(i.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, i.config.analyticsRate))
	}
	span, ctx := tracer.StartSpanFromContext(ctx, i.config.querySpanName, opts...)

	return ctx, func(result *graphql.Result) {
		err := toError(result.Errors)
		defer op.Finish(graphqlsec.QueryResult{
			Data:  result.Data,
			Error: err,
		})

		span.Finish(tracer.WithError(err))
	}
}

// ResolveFieldDidStart notifies about the start of the resolving of a field
func (i datadogExtension) ResolveFieldDidStart(ctx context.Context, info *graphql.ResolveInfo) (context.Context, graphql.ResolveFieldFinishFunc) {
	ctx, op := graphqlsec.StartField(ctx, graphqlsec.FieldArguments{
		FieldName: info.FieldName,
		TypeName:  info.ParentType.Name(),
		Arguments: info.VariableValues,
		// TODO(romain.marcadier): More fields?
	})

	var operationName string
	switch def := info.Operation.(type) {
	case *ast.OperationDefinition:
		if def.Name != nil {
			operationName = def.Name.Value
		}
	case *ast.FragmentDefinition:
		if def.Name != nil {
			operationName = def.Name.Value
		}
	default:
		operationName = info.FieldName
	}

	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(i.config.serviceName),
		tracer.Tag(tagGraphqlField, info.FieldName),
		tracer.Tag(tagGraphqlType, info.Operation.GetOperation()),
		tracer.Tag(ext.Component, componentName),
		tracer.Measured(),
	}
	if operationName != "" {
		opts = append(opts, tracer.Tag(tagGraphqlOperationName, operationName))
	}
	if !math.IsNaN(i.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, i.config.analyticsRate))
	}

	span, ctx := tracer.StartSpanFromContext(ctx, "graphql.field", opts...)

	return ctx, func(result any, err error) {
		defer op.Finish(graphqlsec.FieldResult{
			Error: err,
			Data:  result,
		})

		span.Finish(tracer.WithError(err))
	}
}

// HasResult returns if the extension wants to add data to the result
func (i datadogExtension) HasResult() bool {
	return false
}

// GetResult returns the data that the extension wants to add to the result
func (i datadogExtension) GetResult(context.Context) interface{} {
	return nil
}

func toError(errs []gqlerrors.FormattedError) error {
	switch count := len(errs); count {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return fmt.Errorf("%w (and %d more errors)", errs[0], count-1)
	}
}
