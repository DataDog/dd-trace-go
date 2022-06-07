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
	// ServiceTagsInjection represents the comment injection mode where only service tags (name, env, version) are injected.
	ServiceTagsInjection SQLCommentInjectionMode = 1
	// FullSQLCommentInjection represents the comment injection mode where both service tags and tracing tags. Tracing tags include span id, trace id and sampling priority.
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

// SQLCommentCarrier is a carrier implementation that injects a span context in a SQL query in the form
// of a sqlcommenter formatted comment prepended to the original query text.
// See https://google.github.io/sqlcommenter/spec/ for more details.
type SQLCommentCarrier struct {
	Query  string
	Mode   SQLCommentInjectionMode
	SpanID uint64
}

// NewSQLCommentCarrier returns a new instance of a SQLCommentCarrier
func NewSQLCommentCarrier(query string, mode SQLCommentInjectionMode) (c *SQLCommentCarrier) {
	c = new(SQLCommentCarrier)
	c.Mode = mode
	c.Query = query
	c.SpanID = random.Uint64()
	return c
}

// Inject injects a span context in the carrier's query.
func (c *SQLCommentCarrier) Inject(spanCtx ddtrace.SpanContext) error {
	if c.Mode == CommentInjectionDisabled {
		return nil
	}

	tags := make(map[string]string)
	if c.Mode == ServiceTagsInjection || c.Mode == FullSQLCommentInjection {
		ctx, ok := spanCtx.(*spanContext)
		var env, version string
		if ok {
			if e, ok := ctx.meta(ext.Environment); ok {
				env = e
			}
			if v, ok := ctx.meta(ext.Version); ok {
				version = v
			}
		}
		if globalconfig.ServiceName() != "" {
			tags[ServiceNameSQLCommentKey] = globalconfig.ServiceName()
		}
		if env != "" {
			tags[ServiceEnvironmentSQLCommentKey] = env
		}
		if version != "" {
			tags[ServiceVersionSQLCommentKey] = version
		}
	}
	if c.Mode == FullSQLCommentInjection {
		samplingPriority := 0

		var traceID uint64
		ctx, ok := spanCtx.(*spanContext)
		if ok {
			if sp, ok := ctx.samplingPriority(); ok {
				samplingPriority = sp
			}
			if ctx.TraceID() > 0 {
				traceID = ctx.TraceID()
			}
		}
		if traceID == 0 {
			traceID = c.SpanID
		}
		tags[TraceIDSQLCommentKey] = strconv.FormatUint(traceID, 10)
		tags[SpanIDSQLCommentKey] = strconv.FormatUint(c.SpanID, 10)
		tags[SamplingPrioritySQLCommentKey] = strconv.Itoa(samplingPriority)
	}

	c.Query = commentQuery(c.Query, tags)
	return nil
}

// commentQuery returns the given query with the tags from the SQLCommentCarrier applied to it as a
// prepended SQL comment. The format of the comment follows the sqlcommenter spec.
// See https://google.github.io/sqlcommenter/spec/ for more details.
func commentQuery(query string, tags map[string]string) string {
	c := serializeTags(tags)
	if c == "" {
		return query
	}
	if query == "" {
		return c
	}
	return fmt.Sprintf("%s %s", c, query)
}

func serializeTags(tags map[string]string) (comment string) {
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
	enc := urlEncode(key)
	return escapeMetaChars(enc)
}

func serializeValue(val string) (encoded string) {
	enc := urlEncode(val)
	escapedMeta := escapeMetaChars(enc)
	return escapeSQL(escapedMeta)
}

func urlEncode(val string) string {
	e := url.QueryEscape(val)
	return strings.Replace(e, "+", "%20", -1)
}

func escapeSQL(value string) (escaped string) {
	return fmt.Sprintf("'%s'", value)
}

func escapeMetaChars(value string) (escaped string) {
	return strings.ReplaceAll(value, "'", "\\'")
}

// Extract is not implemented on SQLCommentCarrier
func (c *SQLCommentCarrier) Extract() (ddtrace.SpanContext, error) {
	return nil, nil
}
