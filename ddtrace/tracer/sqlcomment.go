// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

// SQLCommentInjectionMode represents the mode of sql comment injection.
type SQLCommentInjectionMode int

const (
	// CommentInjectionDisabled represents the comment injection mode where all injection is disabled.
	CommentInjectionDisabled SQLCommentInjectionMode = 0
	// StaticTagsSQLCommentInjection represents the comment injection mode where only static tags are injected. Static tags include values that are set once during the lifetime of an application: service name, env, version.
	StaticTagsSQLCommentInjection SQLCommentInjectionMode = 1
	// FullSQLCommentInjection represents the comment injection mode where both static and dynamic tags are injected. Dynamic tags include values like span id, trace id and sampling priority.
	FullSQLCommentInjection SQLCommentInjectionMode = 2
)

// Key names for SQL comment tags.
const (
	SamplingPrioritySQLCommentKey   = "ddsp"
	TraceIDSQLCommentKey            = "ddtid"
	SpanIDSQLCommentKey             = "ddsid"
	ServiceNameSQLCommentKey        = "ddsn"
	ServiceVersionSQLCommentKey     = "ddsv"
	ServiceEnvironmentSQLCommentKey = "dde"
)

// QueryCommentCarrier is a more specific interface implemented by carriers that implement the TextMapWriter
// as well as CommentQuery, AddSpanID and SetDynamicTag methods. It is compatible with Datadog's SQLCommentPropagator.
// Note that Datadog's TextMapPropagator is compatible with QueryCommentCarriers but only in the fact that it
// ignores it without returning errors.
type QueryCommentCarrier interface {
	TextMapWriter

	// SetDynamicTag sets the given dynamic key/value pair. This method is a variation on TextMapWriter's Set method
	// that exists for dynamic tag values.
	SetDynamicTag(key, val string)

	// CommentQuery returns the given query with any injected tags prepended in the form of a sqlcommenter-formatted
	// SQL comment. It also returns a non-zero span ID if a new span ID was generated as part of the inject operation.
	// See https://google.github.io/sqlcommenter/spec for the full sqlcommenter spec.
	CommentQuery(query string) (commented string, spanID uint64)

	// AddSpanID adds a new span ID to the carrier. This happens if no existing trace is found when the
	// injection happens.
	AddSpanID(spanID uint64)
}

// SQLCommentPropagator implements the Propagator interface to inject tags
// in sql comments. It is only compatible with QueryCommentCarrier implementations
// but allows other carriers to flow through without returning errors.
type SQLCommentPropagator struct {
	mode SQLCommentInjectionMode
}

// SQLCommentWithDynamicTagsDiscarded enables control discarding dynamic tags on a SQLCommentCarrier.
// Its main purpose is to allow discarding dynamic tags per SQL operation when they aren't relevant
// (i.e. Prepared statements).
func SQLCommentWithDynamicTagsDiscarded(discard bool) SQLCommentCarrierOption {
	return func(c *SQLCommentCarrierConfig) {
		c.discardDynamicTags = discard
	}
}

// NewSQLCommentPropagator returns a new SQLCommentPropagator with the given injection mode
func NewSQLCommentPropagator(mode SQLCommentInjectionMode) *SQLCommentPropagator {
	return &SQLCommentPropagator{mode: mode}
}

// Inject injects the span context in the given carrier. Note that it is only compatible
// with QueryCommentCarriers and no-ops if the carrier is of any other type.
func (p *SQLCommentPropagator) Inject(spanCtx ddtrace.SpanContext, carrier interface{}) error {
	switch c := carrier.(type) {
	case QueryCommentCarrier:
		return p.injectWithCommentCarrier(spanCtx, c)
	default:
		// SQLCommentPropagator only handles QueryCommentCarrier carriers but lets any other carrier
		// flow through without returning errors
		return nil
	}
}

func (p *SQLCommentPropagator) injectWithCommentCarrier(spanCtx ddtrace.SpanContext, carrier QueryCommentCarrier) error {
	if p.mode == CommentInjectionDisabled {
		return nil
	}
	if p.mode == StaticTagsSQLCommentInjection || p.mode == FullSQLCommentInjection {
		ctx, ok := spanCtx.(*spanContext)
		var env, pversion string
		if ok {
			if e, ok := ctx.meta(ext.Environment); ok {
				env = e
			}
			if version, ok := ctx.meta(ext.ParentVersion); ok {
				pversion = version
			}
		}
		if globalconfig.ServiceName() != "" {
			carrier.Set(ServiceNameSQLCommentKey, globalconfig.ServiceName())
		}
		if env != "" {
			carrier.Set(ServiceEnvironmentSQLCommentKey, env)
		}
		if pversion != "" {
			carrier.Set(ServiceVersionSQLCommentKey, pversion)
		}
	}
	if p.mode == FullSQLCommentInjection {
		samplingPriority := 0
		var traceID, spanID uint64

		ctx, ok := spanCtx.(*spanContext)
		if ok {
			if sp, ok := ctx.samplingPriority(); ok {
				samplingPriority = sp
			}
			if ctx.TraceID() > 0 {
				traceID = ctx.TraceID()
			}
			if ctx.SpanID() > 0 {
				spanID = ctx.SpanID()
			}
		}
		if spanID == 0 {
			spanID = random.Uint64()
			carrier.AddSpanID(spanID)
		}
		if traceID == 0 {
			traceID = spanID
		}
		carrier.SetDynamicTag(TraceIDSQLCommentKey, strconv.FormatUint(traceID, 10))
		carrier.SetDynamicTag(SpanIDSQLCommentKey, strconv.FormatUint(spanID, 10))
		carrier.SetDynamicTag(SamplingPrioritySQLCommentKey, strconv.Itoa(samplingPriority))
	}
	return nil
}

// Extract is not implemented for the SQLCommentPropagator.
func (p *SQLCommentPropagator) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	return nil, fmt.Errorf("not implemented")
}

// SQLCommentCarrierConfig holds configuration for a SQLCommentCarrier
type SQLCommentCarrierConfig struct {
	discardDynamicTags bool
}

// SQLCommentCarrierOption represents a function that can be provided as a parameter to NewSQLCommentCarrier.
type SQLCommentCarrierOption func(c *SQLCommentCarrierConfig)

// SQLCommentCarrier implements QueryCommentCarrier by holding tags, configuration
// and a potential new span id to generate a SQL comment injected in queries.
type SQLCommentCarrier struct {
	tags   map[string]string
	cfg    SQLCommentCarrierConfig
	spanID uint64
}

// NewSQLCommentCarrier returns a new SQLCommentCarrier.
func NewSQLCommentCarrier(opts ...SQLCommentCarrierOption) (s *SQLCommentCarrier) {
	s = new(SQLCommentCarrier)
	for _, apply := range opts {
		apply(&s.cfg)
	}

	return s
}

// Set implements TextMapWriter. In the context of SQL comment injection, this method
// is used for static tags only. See SetDynamicTag for dynamic tags.
func (c *SQLCommentCarrier) Set(key, val string) {
	if c.tags == nil {
		c.tags = make(map[string]string)
	}
	c.tags[key] = val
}

// ForeachKey implements TextMapReader.
func (c SQLCommentCarrier) ForeachKey(handler func(key, val string) error) error {
	for k, v := range c.tags {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

// SetDynamicTag implements QueryCommentCarrier. This method is used to inject dynamic tags only
// (i.e. span id, trace id, sampling priority). See Set for static tags.
func (c *SQLCommentCarrier) SetDynamicTag(key, val string) {
	if c.cfg.discardDynamicTags {
		return
	}
	if c.tags == nil {
		c.tags = make(map[string]string)
	}
	c.tags[key] = val
}

// AddSpanID implements QueryCommentCarrier and is used to save a span id generated during SQL comment injection
// in the case where there is no active trace.
func (c *SQLCommentCarrier) AddSpanID(spanID uint64) {
	c.spanID = spanID
}

// CommentQuery returns the given query with the tags from the SQLCommentCarrier applied to it as a
// prepended SQL comment. The format of the comment follows the sqlcommenter spec.
// See https://google.github.io/sqlcommenter/spec/ for more details.
func (c *SQLCommentCarrier) CommentQuery(query string) (commented string, spanID uint64) {
	comment := commentWithTags(c.tags)
	if comment == "" {
		return query, c.spanID
	}
	if query == "" {
		return comment, c.spanID
	}
	return fmt.Sprintf("%s %s", comment, query), c.spanID
}

func commentWithTags(tags map[string]string) (comment string) {
	if len(tags) == 0 {
		return ""
	}
	serializedTags := make([]string, 0, len(tags))
	for k, v := range tags {
		serializedTags = append(serializedTags, serializeTag(k, v))
	}
	sort.Strings(serializedTags)
	comment = strings.Join(serializedTags, ",")
	return fmt.Sprintf("/*%s*/", comment)
}

func serializeTag(key string, value string) (serialized string) {
	sKey := serializeKey(key)
	sValue := serializeValue(value)

	return fmt.Sprintf("%s=%s", sKey, sValue)
}

func serializeKey(key string) (encoded string) {
	urlEncoded := url.PathEscape(key)
	escapedMeta := escapeMetaChars(urlEncoded)

	return escapedMeta
}

func serializeValue(value string) (encoded string) {
	urlEncoded := url.PathEscape(value)
	escapedMeta := escapeMetaChars(urlEncoded)
	escaped := escapeSQL(escapedMeta)

	return escaped
}

func escapeSQL(value string) (escaped string) {
	return fmt.Sprintf("'%s'", value)
}

func escapeMetaChars(value string) (escaped string) {
	return strings.ReplaceAll(value, "'", "\\'")
}
