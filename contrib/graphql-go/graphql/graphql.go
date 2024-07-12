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
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/graphqlsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/gqlerrors"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/hashicorp/go-multierror"
)

const componentName = "graphql-go/graphql"

var (
	spanTagKind = tracer.Tag(ext.SpanKind, ext.SpanKindServer)
	spanTagType = tracer.Tag(ext.SpanType, ext.SpanTypeGraphQL)
)

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/graphql-go/graphql")
}

const (
	spanServer              = "graphql.server"
	spanParse               = "graphql.parse"
	spanValidate            = "graphql.validate"
	spanExecute             = "graphql.execute"
	spanResolve             = "graphql.resolve"
	tagGraphqlField         = "graphql.field"
	tagGraphqlOperationName = "graphql.operation.name"
	tagGraphqlOperationType = "graphql.operation.type"
	tagGraphqlSource        = "graphql.source"
	tagGraphqlVariables     = "graphql.variables"
)

func NewSchema(config graphql.SchemaConfig, options ...Option) (graphql.Schema, error) {
	extension := datadogExtension{}
	defaults(&extension.config)
	for _, opt := range options {
		opt(&extension.config)
	}
	config.Extensions = append(config.Extensions, extension)
	return graphql.NewSchema(config)
}

type datadogExtension struct{ config }

type contextKey struct{}
type contextData struct {
	serverSpan    tracer.Span
	requestOp     *types.RequestOperation
	variables     map[string]any
	query         string
	operationName string
}

// finish closes the top-level request operation, as well as the server span.
func (c *contextData) finish(data any, err error) {
	defer c.serverSpan.Finish(tracer.WithError(err))
	c.requestOp.Finish(types.RequestOperationRes{Data: data, Error: err})
}

var extensionName = reflect.TypeOf((*datadogExtension)(nil)).Elem().Name()

// Init is used to help you initialize the extension
func (i datadogExtension) Init(ctx context.Context, params *graphql.Params) context.Context {
	if ctx == nil {
		// No init context is available, attempt to fall back to a suitable alternative...
		if params.Context != nil {
			ctx = params.Context
		} else {
			// In case we didn't get a user context, use a stand-in context.TODO
			ctx = context.TODO()
		}
	}
	// This span allows us to regroup parse, validate & resolvers under a single service entry span. It is finished once
	// the execution is done (or after parse or validate have failed).
	span, ctx := tracer.StartSpanFromContext(ctx, spanServer,
		tracer.ServiceName(i.config.serviceName),
		spanTagKind,
		spanTagType,
		tracer.Tag(ext.Component, componentName),
		tracer.Measured(),
	)
	ctx, request := graphqlsec.StartRequestOperation(ctx, span, types.RequestOperationArgs{
		RawQuery:      params.RequestString,
		Variables:     params.VariableValues,
		OperationName: params.OperationName,
	})
	return context.WithValue(ctx, contextKey{}, contextData{
		query:         params.RequestString,
		operationName: params.OperationName,
		variables:     params.VariableValues,
		serverSpan:    span,
		requestOp:     request,
	})
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
		spanTagKind,
		spanTagType,
		tracer.Tag(tagGraphqlSource, data.query),
		tracer.Tag(ext.Component, componentName),
		tracer.Measured(),
	}
	if data.operationName != "" {
		opts = append(opts, tracer.Tag(tagGraphqlOperationName, data.operationName))
	}
	if !math.IsNaN(i.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, i.config.analyticsRate))
	}
	span, ctx := tracer.StartSpanFromContext(ctx, spanParse, opts...)
	return ctx, func(err error) {
		span.Finish(tracer.WithError(err))
		if err != nil {
			// There were errors, so the query will not be executed, finish the graphql.server span now.
			data.finish(nil, err)
		}
	}
}

// ValidationDidStart is called just before the validation begins
func (i datadogExtension) ValidationDidStart(ctx context.Context) (context.Context, graphql.ValidationFinishFunc) {
	data, _ := ctx.Value(contextKey{}).(contextData)
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(i.config.serviceName),
		spanTagKind,
		spanTagType,
		tracer.Tag(tagGraphqlSource, data.query),
		tracer.Tag(ext.Component, componentName),
		tracer.Measured(),
	}
	if data.operationName != "" {
		opts = append(opts, tracer.Tag(tagGraphqlOperationName, data.operationName))
	}
	if !math.IsNaN(i.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, i.config.analyticsRate))
	}
	span, ctx := tracer.StartSpanFromContext(ctx, spanValidate, opts...)
	return ctx, func(errs []gqlerrors.FormattedError) {
		span.Finish(tracer.WithError(toError(errs)))
		if len(errs) > 0 {
			var err error = errs[0]
			for _, e := range errs[1:] {
				err = multierror.Append(err, e)
			}
			// There were errors, so the query will not be executed, finish the graphql.server span now.
			data.finish(nil, err)
		}
	}
}

// ExecutionDidStart notifies about the start of the execution
func (i datadogExtension) ExecutionDidStart(ctx context.Context) (context.Context, graphql.ExecutionFinishFunc) {
	data, _ := ctx.Value(contextKey{}).(contextData)
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(i.config.serviceName),
		spanTagKind,
		spanTagType,
		tracer.Tag(tagGraphqlSource, data.query),
		tracer.Tag(ext.Component, componentName),
		tracer.Measured(),
	}
	if data.operationName != "" {
		opts = append(opts, tracer.Tag(tagGraphqlOperationName, data.operationName))
	}
	if !math.IsNaN(i.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, i.config.analyticsRate))
	}
	span, ctx := tracer.StartSpanFromContext(ctx, spanExecute, opts...)
	ctx, op := graphqlsec.StartExecutionOperation(ctx, span, types.ExecutionOperationArgs{
		Query:         data.query,
		OperationName: data.operationName,
		Variables:     data.variables,
	})
	return ctx, func(result *graphql.Result) {
		err := toError(result.Errors)
		defer func() {
			defer data.finish(result.Data, err)
			span.Finish(tracer.WithError(err))
		}()
		op.Finish(types.ExecutionOperationRes{Data: result.Data, Error: err})
	}
}

// ResolveFieldDidStart notifies about the start of the resolving of a field
func (i datadogExtension) ResolveFieldDidStart(ctx context.Context, info *graphql.ResolveInfo) (context.Context, graphql.ResolveFieldFinishFunc) {
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
		spanTagKind,
		spanTagType,
		tracer.Tag(tagGraphqlField, info.FieldName),
		tracer.Tag(tagGraphqlOperationType, info.Operation.GetOperation()),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.ResourceName, fmt.Sprintf("%s.%s", info.ParentType.Name(), info.FieldName)),
		tracer.Measured(),
	}
	if operationName != "" {
		opts = append(opts, tracer.Tag(tagGraphqlOperationName, operationName))
	}
	if !math.IsNaN(i.config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, i.config.analyticsRate))
	}
	span, ctx := tracer.StartSpanFromContext(ctx, spanResolve, opts...)
	ctx, op := graphqlsec.StartResolveOperation(ctx, span, types.ResolveOperationArgs{
		TypeName:  info.ParentType.Name(),
		FieldName: info.FieldName,
		Arguments: collectArguments(info),
	})
	return ctx, func(result any, err error) {
		defer span.Finish(tracer.WithError(err))
		op.Finish(types.ResolveOperationRes{Error: err, Data: result})
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

func collectArguments(info *graphql.ResolveInfo) map[string]any {
	var args map[string]any
	for _, field := range info.FieldASTs {
		if args == nil && len(field.Arguments) > 0 {
			args = make(map[string]any, len(field.Arguments))
		}
		for _, arg := range field.Arguments {
			argName := arg.Name.Value
			argValue := resolveValue(arg.Value, info.VariableValues)
			args[argName] = argValue
		}
	}
	return args
}

func resolveValue(value ast.Value, variableValues map[string]any) any {
	switch value := value.(type) {
	case *ast.Variable:
		varName := value.GetValue().(*ast.Name).Value
		return variableValues[varName]
	case *ast.ObjectValue:
		fields := make(map[string]any, len(value.Fields))
		for _, field := range value.Fields {
			fields[field.Name.Value] = resolveValue(field.Value, variableValues)
		}
		return fields
	case *ast.ListValue:
		items := make([]any, len(value.Values))
		for i, item := range value.Values {
			items[i] = resolveValue(item, variableValues)
		}
		return items
	default:
		// Note - *ast.IntValue and *ast.FloatValue both use a string representation here. This is okay.
		return value.GetValue()
	}
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
