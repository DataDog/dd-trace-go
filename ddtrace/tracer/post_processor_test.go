package tracer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

func TestTag(t *testing.T) {
	for name, tc := range map[string]struct {
		key  string
		val  interface{}
		want interface{}
	}{
		"name":     {key: ext.SpanName, val: "http.request", want: "http.request"},
		"service":  {key: ext.ServiceName, val: "test_svc", want: "test_svc"},
		"resource": {key: ext.ResourceName, val: "GET /example", want: "GET /example"},
		"type":     {key: ext.SpanType, val: "web", want: "web"},

		"manual-keep":                   {key: ext.ManualKeep, val: true, want: true},
		"manual-keep-v1":                {key: keySamplingPriority, val: 2, want: float64(2)},
		"manual-keep-sampling-priority": {key: ext.SamplingPriority, val: 2, want: float64(2)},
		"manual-drop":                   {key: ext.ManualDrop, val: true, want: true},
		"manual-drop-v1":                {key: keySamplingPriority, val: -1, want: float64(-1)},
		"manual-drop-sampling-priority": {key: ext.SamplingPriority, val: -1, want: float64(-1)},

		"error-msg":     {key: ext.ErrorMsg, val: "abc", want: "abc"},
		"error-type":    {key: ext.ErrorType, val: "*errors.errorString", want: "*errors.errorString"},
		"error-stack":   {key: ext.ErrorStack, val: "stack", want: "stack"},
		"error-details": {key: ext.ErrorDetails, val: "error-details", want: "error-details"},

		"meta":              {key: "custom", val: "value", want: "value"},
		"float64":           {key: "user.id", val: 1234, want: float64(1234)},
		"bool-true":         {key: "test_true", val: true, want: "true"},
		"bool-false":        {key: "test_false", val: false, want: "false"},
		"bool-true-string":  {key: "test_true_string", val: "true", want: "true"},
		"bool-false-string": {key: "test_false_string", val: "false", want: "false"},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			span := newBasicSpan("")
			s := readWriteSpan{span}
			span.SetTag(tc.key, tc.val)
			assert.Equal(tc.want, s.Tag(tc.key))
		})
	}

	t.Run("no-tag", func(t *testing.T) {
		assert := assert.New(t)
		span := newBasicSpan("")
		s := readWriteSpan{span}
		assert.Equal(nil, s.Tag("no-tag"))
	})
}

func TestFieldGetters(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		assert := assert.New(t)
		span := newBasicSpan("")
		s := readWriteSpan{span}
		assert.Equal("", s.GetName())
		assert.Equal("", s.GetService())
		assert.Equal("", s.GetResource())
		assert.Equal("", s.GetType())
	})

	t.Run("set", func(t *testing.T) {
		assert := assert.New(t)
		span := newSpan("http.request", "test_svc", "GET /example", 0, 0, 0)
		span.Type = "web"
		s := readWriteSpan{span}
		assert.Equal("http.request", s.GetName())
		assert.Equal("test_svc", s.GetService())
		assert.Equal("GET /example", s.GetResource())
		assert.Equal("web", s.GetType())
	})
}

func TestErrorGetters(t *testing.T) {
	t.Run("no-error", func(t *testing.T) {
		assert := assert.New(t)
		span := newBasicSpan("http.request")
		s := readWriteSpan{span}
		assert.Equal(false, s.IsError())
		assert.Equal("", s.ErrorMessage())
		assert.Equal("", s.ErrorType())
		assert.Equal("", s.ErrorStack())
	})

	t.Run("no-error", func(t *testing.T) {
		assert := assert.New(t)
		span := newBasicSpan("http.request")
		s := readWriteSpan{span}
		span.SetTag(ext.Error, errors.New("abc"))
		assert.Equal(true, s.IsError())
		assert.Equal("abc", s.ErrorMessage())
		assert.Equal("*errors.errorString", s.ErrorType())
		assert.Equal("", s.ErrorDetails())
		assert.NotEmpty(s.ErrorStack())
	})
}

func TestSetTag(t *testing.T) {
	for name, tc := range map[string]struct {
		key      string
		val      interface{}
		rWSetVal interface{}
		want     interface{}
	}{
		"name":              {key: ext.SpanName, val: "http.request", rWSetVal: "changed.name"},
		"type":              {key: ext.SpanType, val: "web", rWSetVal: "db"},
		"resource":          {key: ext.ResourceName, val: "GET /example", rWSetVal: "POST /datadog"},
		"service":           {key: ext.ServiceName, val: "test_svc", rWSetVal: "new_svc"},
		"status_code":       {key: ext.HTTPCode, val: "200", rWSetVal: "404"},
		"env":               {key: ext.Environment, val: "prod", rWSetVal: "breaking"},
		"measured":          {key: keyMeasured, val: 0, rWSetVal: 1, want: float64(0)},
		"keyTopLevel":       {key: keyTopLevel, val: 0, rWSetVal: 1, want: float64(0)},
		"ManualKeep":        {key: ext.ManualKeep, val: true, rWSetVal: false},
		"ManualDrop":        {key: ext.ManualDrop, val: true, rWSetVal: false},
		"analytics":         {key: ext.AnalyticsEvent, val: true, rWSetVal: true, want: true},
		"analytics-rate":    {key: ext.EventSampleRate, val: 0, rWSetVal: 1, want: float64(0)},
		"sampling-priority": {key: ext.SamplingPriority, val: 1, rWSetVal: 4, want: float64(1)},
		"sampling-v1":       {key: keySamplingPriority, val: 1, rWSetVal: 4, want: float64(1)},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			span := newBasicSpan("")
			s := readWriteSpan{span}
			span.SetTag(tc.key, tc.val)
			s.SetTag(tc.key, tc.rWSetVal)
			if tc.want == nil {
				assert.Equal(tc.val, s.Tag(tc.key))
			} else {
				assert.Equal(tc.want, s.Tag(tc.key))
			}
		})
	}

	t.Run("allowed-tags", func(t *testing.T) {
		assert := assert.New(t)
		span := newBasicSpan("")
		s := readWriteSpan{span}
		s.SetTag("custom_string", "value")
		assert.Equal("value", s.Tag("custom_string"))
		s.SetTag("custom_int", 1234)
		assert.Equal(float64(1234), s.Tag("custom_int"))
		s.SetTag("custom_bool", true)
		assert.Equal("true", s.Tag("custom_bool"))
	})
}

func TestNewReadWriteSpanSlice(t *testing.T) {
	assert := assert.New(t)
	spans := []*span{newBasicSpan("http.request"), newBasicSpan("db.request")}
	rWSpans := newReadWriteSpanSlice(spans)
	for i, s := range rWSpans {
		assert.Equal(s.(readWriteSpan).span, spans[i])
		assert.Same(s.(readWriteSpan).span, spans[i])
	}
}
