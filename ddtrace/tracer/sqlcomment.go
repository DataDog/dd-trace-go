// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
	"net/url"
	"sort"
	"strconv"
	"strings"
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

type SQLCommentCarrier struct {
	Query  string
	Mode   SQLCommentInjectionMode
	SpanID uint64
}

func NewSQLCommentCarrier(query string, mode SQLCommentInjectionMode) (s *SQLCommentCarrier) {
	s = new(SQLCommentCarrier)
	s.Mode = mode
	s.Query = query
	return s
}

func (c *SQLCommentCarrier) Inject(spanCtx ddtrace.SpanContext) error {
	c.SpanID = random.Uint64()
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

// Extract parses the first sql comment found in the query text and returns the span context with values
// extracted from tags extracted for the sql comment.
func (c *SQLCommentCarrier) Extract() (ddtrace.SpanContext, error) {
	cmt, err := findSQLComment(c.Query)
	if err != nil {
		return nil, err
	}
	if cmt == "" {
		return nil, nil
	}
	tags, err := extractCommentTags(cmt)
	if err != nil {
		return nil, fmt.Errorf("unable to extract tags from comment [%s]: %w", cmt, err)
	}
	var spid uint64
	if v := tags[SpanIDSQLCommentKey]; v != "" {
		spid, err = strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("unable to parse span id [%s]: %w", v, err)
		}
	}
	var tid uint64
	if v := tags[TraceIDSQLCommentKey]; v != "" {
		tid, err = strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("unable to parse trace id [%s]: %w", v, err)
		}
	}
	svc := tags[ServiceNameSQLCommentKey]
	ctx := newSpanContext(&span{
		Service: svc,
		Meta: map[string]string{
			ext.Version:     tags[ServiceVersionSQLCommentKey],
			ext.Environment: tags[ServiceEnvironmentSQLCommentKey],
		},
		SpanID:  spid,
		TraceID: tid,
	}, nil)
	if v := tags[SamplingPrioritySQLCommentKey]; v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return nil, err
		}
		ctx.trace = newTrace()
		ctx.trace.setSamplingPriority(svc, p, samplernames.Default, 1)
	}
	return ctx, nil
}

func findSQLComment(query string) (comment string, err error) {
	start := strings.Index(query, "/*")
	if start == -1 {
		return "", nil
	}
	end := strings.Index(query[start:], "*/")
	if end == -1 {
		return "", nil
	}
	c := query[start : end+2]
	spacesTrimmed := strings.TrimSpace(c)
	if !strings.HasPrefix(spacesTrimmed, "/*") {
		return "", fmt.Errorf("comments not in the sqlcommenter format, expected to start with '/*'")
	}
	if !strings.HasSuffix(spacesTrimmed, "*/") {
		return "", fmt.Errorf("comments not in the sqlcommenter format, expected to end with '*/'")
	}
	c = strings.TrimLeft(c, "/*")
	c = strings.TrimRight(c, "*/")
	return strings.TrimSpace(c), nil
}

func extractCommentTags(comment string) (keyValues map[string]string, err error) {
	keyValues = make(map[string]string)
	if err != nil {
		return nil, err
	}
	if comment == "" {
		return keyValues, nil
	}
	tagList := strings.Split(comment, ",")
	for _, t := range tagList {
		k, v, err := extractKeyValue(t)
		if err != nil {
			return nil, err
		} else {
			keyValues[k] = v
		}
	}
	return keyValues, nil
}

func extractKeyValue(tag string) (key string, val string, err error) {
	parts := strings.SplitN(tag, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("tag format invalid, expected 'key=value' but got %s", tag)
	}
	key, err = extractKey(parts[0])
	if err != nil {
		return "", "", err
	}
	val, err = extractValue(parts[1])
	if err != nil {
		return "", "", err
	}
	return key, val, nil
}

func extractKey(keyVal string) (key string, err error) {
	unescaped := unescapeMetaCharacters(keyVal)
	decoded, err := url.PathUnescape(unescaped)
	if err != nil {
		return "", fmt.Errorf("failed to url unescape key: %w", err)
	}

	return decoded, nil
}

func extractValue(rawValue string) (value string, err error) {
	trimmedLeft := strings.TrimLeft(rawValue, "'")
	trimmed := strings.TrimRight(trimmedLeft, "'")

	unescaped := unescapeMetaCharacters(trimmed)
	decoded, err := url.PathUnescape(unescaped)

	if err != nil {
		return "", fmt.Errorf("failed to url unescape value: %w", err)
	}

	return decoded, nil
}

func unescapeMetaCharacters(val string) (unescaped string) {
	return strings.ReplaceAll(val, "\\'", "'")
}
