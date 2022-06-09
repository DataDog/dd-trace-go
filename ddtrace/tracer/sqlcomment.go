// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

// SQLCommentInjectionMode represents the mode of SQL comment injection.
type SQLCommentInjectionMode int

const (
	// SQLInjectionDisabled represents the comment injection mode where all injection is disabled.
	SQLInjectionDisabled SQLCommentInjectionMode = 0
	// SQLInjectionModeService represents the comment injection mode where only service tags (name, env, version) are injected.
	SQLInjectionModeService SQLCommentInjectionMode = 1
	// SQLInjectionModeFull represents the comment injection mode where both service tags and tracing tags. Tracing tags include span id, trace id and sampling priority.
	SQLInjectionModeFull SQLCommentInjectionMode = 2
)

// Key names for SQL comment tags.
const (
	sqlCommentKeySamplingPriority = "ddsp"
	sqlCommentTraceID             = "ddtid"
	sqlCommentSpanID              = "ddsid"
	sqlCommentService             = "ddsn"
	sqlCommentVersion             = "ddsv"
	sqlCommentEnv                 = "dde"
)

// SQLCommentCarrier is a carrier implementation that injects a span context in a SQL query in the form
// of a sqlcommenter formatted comment prepended to the original query text.
// See https://google.github.io/sqlcommenter/spec/ for more details.
type SQLCommentCarrier struct {
	Query  string
	Mode   SQLCommentInjectionMode
	SpanID uint64
}

// Inject injects a span context in the carrier's query.
func (c *SQLCommentCarrier) Inject(spanCtx ddtrace.SpanContext) error {
	c.SpanID = random.Uint64()
	tags := make(map[string]string)
	switch c.Mode {
	case SQLInjectionDisabled:
		return nil
	case SQLInjectionModeFull:
		var (
			samplingPriority int
			traceID          uint64
		)
		if ctx, ok := spanCtx.(*spanContext); ok {
			if sp, ok := ctx.samplingPriority(); ok {
				samplingPriority = sp
			}
			traceID = ctx.TraceID()
		}
		if traceID == 0 {
			traceID = c.SpanID
		}
		tags[sqlCommentTraceID] = strconv.FormatUint(traceID, 10)
		tags[sqlCommentSpanID] = strconv.FormatUint(c.SpanID, 10)
		tags[sqlCommentKeySamplingPriority] = strconv.Itoa(samplingPriority)
		fallthrough
	case SQLInjectionModeService:
		var env, version string
		if ctx, ok := spanCtx.(*spanContext); ok {
			if e, ok := ctx.meta(ext.Environment); ok {
				env = e
			}
			if v, ok := ctx.meta(ext.Version); ok {
				version = v
			}
		}
		if globalconfig.ServiceName() != "" {
			tags[sqlCommentService] = globalconfig.ServiceName()
		}
		if env != "" {
			tags[sqlCommentEnv] = env
		}
		if version != "" {
			tags[sqlCommentVersion] = version
		}
	}
	c.Query = commentQuery(c.Query, tags)
	return nil
}

// commentQuery returns the given query with the tags from the SQLCommentCarrier applied to it as a
// prepended SQL comment. The format of the comment follows the sqlcommenter spec.
// See https://google.github.io/sqlcommenter/spec/ for more details.
func commentQuery(query string, tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	serializedTags := make([]string, 0, len(tags))
	// the sqlcommenter specification dictates that tags should be sorted. Since we know all injected keys,
	// we skip a sorting operation by specifying the order of keys statically
	orderedKeys := []string{sqlCommentEnv, sqlCommentSpanID, sqlCommentService, sqlCommentKeySamplingPriority, sqlCommentVersion, sqlCommentTraceID}
	for _, k := range orderedKeys {
		if v, ok := tags[k]; ok {
			// we need to URL-encode all + characters and escape single quotes
			// https://google.github.io/sqlcommenter/spec/
			key := strings.Replace(url.QueryEscape(k), "+", "%20", -1)
			key = strings.ReplaceAll(key, "'", "\\'")
			val := strings.Replace(url.QueryEscape(v), "+", "%20", -1)
			val = strings.ReplaceAll(val, "'", "\\'")
			serializedTags = append(serializedTags, fmt.Sprintf("%s='%s'", key, val))
		}
	}
	cmt := fmt.Sprintf("/*%s*/", strings.Join(serializedTags, ","))
	if cmt == "" {
		return query
	}
	if query == "" {
		return cmt
	}
	return fmt.Sprintf("%s %s", cmt, query)
}

// Extract is not implemented on SQLCommentCarrier
func (c *SQLCommentCarrier) Extract() (ddtrace.SpanContext, error) {
	return nil, nil
}
