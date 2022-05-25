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
	CommentInjectionDisabled SQLCommentInjectionMode = iota
	// StaticTagsSQLCommentInjection represents the comment injection mode where only static tags are injected. Static tags include values that are set once during the lifetime of an application: service name, env, version.
	StaticTagsSQLCommentInjection
	// FullSQLCommentInjection represents the comment injection mode where both static and dynamic tags are injected. Dynamic tags include values like span id, trace id and sampling priority.
	FullSQLCommentInjection
)

// Values for sql comment keys
const (
	SamplingPrioritySQLCommentKey   = "ddsp"
	TraceIDSQLCommentKey            = "ddtid"
	SpanIDSQLCommentKey             = "ddsid"
	ServiceNameSQLCommentKey        = "ddsn"
	ServiceVersionSQLCommentKey     = "ddsv"
	ServiceEnvironmentSQLCommentKey = "dde"
)

// QueryCommenter is a more specific interface implemented by carrier that implement the TextMapWriter
// as well as CommentQuery and AddSpanID methods
type QueryCommenter interface {
	TextMapWriter
	SetDynamicTag(key, val string)
	CommentQuery(query string) (commented string, spanID uint64)
	AddSpanID(spanID uint64)
}

// SQLCommentPropagator implements the Propagator interface to inject tags
// in sql comments
type SQLCommentPropagator struct {
	mode SQLCommentInjectionMode
}

func CommentWithDynamicTagsDiscarded(discard bool) SQLCommentCarrierOption {
	return func(c *SQLCommentCarrierConfig) {
		c.discardDynamicTags = discard
	}
}

func NewCommentPropagator(mode SQLCommentInjectionMode) *SQLCommentPropagator {
	return &SQLCommentPropagator{mode: mode}
}

func (p *SQLCommentPropagator) Inject(spanCtx ddtrace.SpanContext, carrier interface{}) error {
	switch c := carrier.(type) {
	case QueryCommenter:
		return p.injectWithCommentCarrier(spanCtx, c)
	default:
		// SQLCommentPropagator only handles QueryCommenter carriers
		return nil
	}
}

func (p *SQLCommentPropagator) injectWithCommentCarrier(spanCtx ddtrace.SpanContext, carrier QueryCommenter) error {
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
		ctx, _ := spanCtx.(*spanContext)
		if sp, ok := ctx.samplingPriority(); ok {
			samplingPriority = sp
		}
		var traceID, spanID uint64
		if ctx.TraceID() > 0 {
			traceID = ctx.TraceID()
		}
		if ctx.SpanID() > 0 {
			spanID = ctx.SpanID()
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

func (p *SQLCommentPropagator) Extract(carrier interface{}) (ddtrace.SpanContext, error) {
	return nil, fmt.Errorf("not implemented")
}

// SQLCommentCarrierConfig holds configuration for a SQLCommentCarrier
type SQLCommentCarrierConfig struct {
	discardDynamicTags bool
}

type SQLCommentCarrierOption func(c *SQLCommentCarrierConfig)

// SQLCommentCarrier holds tags to be serialized as a SQL Comment
type SQLCommentCarrier struct {
	tags   map[string]string
	cfg    SQLCommentCarrierConfig
	spanID uint64
}

// NewSQLCommentCarrier returns a new SQLCommentCarrier
func NewSQLCommentCarrier(opts ...SQLCommentCarrierOption) (s *SQLCommentCarrier) {
	s = new(SQLCommentCarrier)
	for _, apply := range opts {
		apply(&s.cfg)
	}

	return s
}

// Set implements TextMapWriter.
func (c *SQLCommentCarrier) Set(key, val string) {
	if c.tags == nil {
		c.tags = make(map[string]string)
	}
	c.tags[key] = val
}

func (c *SQLCommentCarrier) SetDynamicTag(key, val string) {
	if c.cfg.discardDynamicTags {
		return
	}
	if c.tags == nil {
		c.tags = make(map[string]string)
	}
	c.tags[key] = val
}

func (c *SQLCommentCarrier) AddSpanID(spanID uint64) {
	c.spanID = spanID
}

func (c *SQLCommentCarrier) SpanID() uint64 {
	return c.spanID
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

// CommentQuery returns the given query with the tags from the SQLCommentCarrier applied to it as a
// prepended SQL comment
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

// ForeachKey implements TextMapReader.
func (c SQLCommentCarrier) ForeachKey(handler func(key, val string) error) error {
	for k, v := range c.tags {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
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
