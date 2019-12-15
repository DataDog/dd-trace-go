// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package tracer

import (
	"io/ioutil"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
)

func TestPrioritySampler(t *testing.T) {
	// create a new span with given service/env
	mkSpan := func(svc, env string) *span {
		s := &span{Service: svc, Meta: map[string]string{}}
		if env != "" {
			s.Meta["env"] = env
		}
		return s
	}

	t.Run("mkspan", func(t *testing.T) {
		assert := assert.New(t)
		s := mkSpan("my-service", "my-env")
		assert.Equal("my-service", s.Service)
		assert.Equal("my-env", s.Meta[ext.Environment])

		s = mkSpan("my-service2", "")
		assert.Equal("my-service2", s.Service)
		_, ok := s.Meta[ext.Environment]
		assert.False(ok)
	})

	t.Run("ops", func(t *testing.T) {
		ps := newPrioritySampler()
		assert := assert.New(t)

		type key struct{ service, env string }
		for _, tt := range []struct {
			in  string
			out map[key]float64
		}{
			{
				in: `{}`,
				out: map[key]float64{
					key{"some-service", ""}:       1,
					key{"obfuscate.http", "none"}: 1,
				},
			},
			{
				in: `{
					"rate_by_service":{
						"service:,env:":0.8,
						"service:obfuscate.http,env:":0.9,
						"service:obfuscate.http,env:none":0.9
					}
				}`,
				out: map[key]float64{
					key{"obfuscate.http", ""}:      0.9,
					key{"obfuscate.http", "none"}:  0.9,
					key{"obfuscate.http", "other"}: 0.8,
					key{"some-service", ""}:        0.8,
				},
			},
			{
				in: `{
					"rate_by_service":{
						"service:my-service,env:":0.2,
						"service:my-service,env:none":0.2
					}
				}`,
				out: map[key]float64{
					key{"my-service", ""}:          0.2,
					key{"my-service", "none"}:      0.2,
					key{"obfuscate.http", ""}:      0.8,
					key{"obfuscate.http", "none"}:  0.8,
					key{"obfuscate.http", "other"}: 0.8,
					key{"some-service", ""}:        0.8,
				},
			},
		} {
			assert.NoError(ps.readRatesJSON(ioutil.NopCloser(strings.NewReader(tt.in))))
			for k, v := range tt.out {
				assert.Equal(v, ps.getRate(mkSpan(k.service, k.env)), k)
			}
		}
	})

	t.Run("race", func(t *testing.T) {
		ps := newPrioritySampler()
		assert := assert.New(t)

		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				assert.NoError(ps.readRatesJSON(
					ioutil.NopCloser(strings.NewReader(
						`{
							"rate_by_service":{
								"service:,env:":0.8,
								"service:obfuscate.http,env:none":0.9
							}
						}`,
					)),
				))
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				ps.getRate(mkSpan("obfuscate.http", "none"))
				ps.getRate(mkSpan("other.service", "none"))
			}
		}()

		wg.Wait()
	})

	t.Run("apply", func(t *testing.T) {
		ps := newPrioritySampler()
		assert := assert.New(t)
		assert.NoError(ps.readRatesJSON(
			ioutil.NopCloser(strings.NewReader(
				`{
					"rate_by_service":{
						"service:obfuscate.http,env:":0.5,
						"service:obfuscate.http,env:none":0.5
					}
				}`,
			)),
		))

		testSpan1 := newBasicSpan("http.request")
		testSpan1.Service = "obfuscate.http"
		testSpan1.TraceID = math.MaxUint64 - (math.MaxUint64 / 4)

		ps.apply(testSpan1)
		assert.EqualValues(ext.PriorityAutoKeep, testSpan1.Metrics[keySamplingPriority])
		assert.EqualValues(0.5, testSpan1.Metrics[keySamplingPriorityRate])

		testSpan1.TraceID = math.MaxUint64 - (math.MaxUint64 / 3)
		ps.apply(testSpan1)
		assert.EqualValues(ext.PriorityAutoReject, testSpan1.Metrics[keySamplingPriority])
		assert.EqualValues(0.5, testSpan1.Metrics[keySamplingPriorityRate])

		testSpan1.Service = "other-service"
		testSpan1.TraceID = 1
		assert.EqualValues(ext.PriorityAutoReject, testSpan1.Metrics[keySamplingPriority])
		assert.EqualValues(0.5, testSpan1.Metrics[keySamplingPriorityRate])
	})
}

func TestRateSampler(t *testing.T) {
	assert := assert.New(t)
	assert.True(NewRateSampler(1).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0.99).Sample(internal.NoopSpan{}))
}

func TestRateSamplerSetting(t *testing.T) {
	assert := assert.New(t)
	rs := NewRateSampler(1)
	assert.Equal(float64(1), rs.Rate())
	rs.SetRate(0.5)
	assert.Equal(float64(0.5), rs.Rate())
}

func TestRuleEnvVars(t *testing.T) {
	t.Run("sample-rate", func(t *testing.T) {
		assert := assert.New(t)
		defer os.Unsetenv("DD_TRACE_SAMPLE_RATE")
		for _, tt := range []struct {
			in  string
			out float64
		}{
			{in: "", out: 0.0},
			{in: "0.0", out: 0.0},
			{in: "0.5", out: 0.5},
			{in: "1.0", out: 1.0},
			{in: "42.0", out: 0.0},    // default if out of range
			{in: "1point0", out: 0.0}, // default if invalid value
		} {
			os.Setenv("DD_TRACE_SAMPLE_RATE", tt.in)
			res := sampleRate()
			assert.Equal(tt.out, res)
		}
	})

	t.Run("rate-limit", func(t *testing.T) {
		assert := assert.New(t)
		defer os.Unsetenv("DD_TRACE_RATE_LIMIT")
		for _, tt := range []struct {
			in  string
			out *rate.Limiter
		}{
			{in: "", out: rate.NewLimiter(rate.Inf, 0)},
			{in: "0.0", out: rate.NewLimiter(0.0, 0)},
			{in: "0.5", out: rate.NewLimiter(0.5, 1)},
			{in: "1.0", out: rate.NewLimiter(1.0, 1)},
			{in: "42.0", out: rate.NewLimiter(42.0, 42)},
			{in: "-1.0", out: rate.NewLimiter(rate.Inf, 0)},    // default if out of range
			{in: "1point0", out: rate.NewLimiter(rate.Inf, 0)}, // default if invalid value
		} {
			os.Setenv("DD_TRACE_RATE_LIMIT", tt.in)
			res := newRateLimiter(1.0)
			assert.Equal(tt.out, res)
		}
	})

	t.Run("sampling-rules", func(t *testing.T) {
		assert := assert.New(t)
		defer os.Unsetenv("DD_TRACE_SAMPLING_RULES")
		// represents hard-coded rules
		rules := []SamplingRule{
			RateRule(1.0),
		}

		// env overrides provided rules
		os.Setenv("DD_TRACE_SAMPLING_RULES", "[]")
		validRules := appliedSamplingRules(rules)
		assert.Len(validRules, 0)

		// valid rules
		os.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "abcd", "rate": 1.0}]`)
		validRules = appliedSamplingRules(rules)
		assert.Len(validRules, 1)

		// invalid rule ignored
		os.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "abcd", "rate": 42.0}]`)
		validRules = appliedSamplingRules(rules)
		assert.Len(validRules, 0)
	})
}

func TestRulesSampler(t *testing.T) {
	makeSpan := func(op string, svc string) *span {
		return newSpan(op, svc, "", 0, 0, 0)
	}

	t.Run("no-rules", func(t *testing.T) {
		assert := assert.New(t)
		rs := newRulesSampler(nil)

		span := makeSpan("http.request", "test-service")
		result := rs.apply(span)
		assert.False(result)
	})

	t.Run("matching", func(t *testing.T) {
		ruleSets := [][]SamplingRule{
			{ServiceRule("test-service", 1.0)},
			{OperationRule("http.request", 1.0)},
			{ServiceOperationRule("test-service", "http.request", 1.0)},
			{{Service: regexp.MustCompile("^test-"), Operation: regexp.MustCompile("http\\..*"), Rate: 1.0}},
			{ServiceRule("other-service-1", 0.0), ServiceRule("other-service-2", 0.0), ServiceRule("test-service", 1.0)},
		}
		for _, v := range ruleSets {
			t.Run("", func(t *testing.T) {
				assert := assert.New(t)
				rs := newRulesSampler(v)

				span := makeSpan("http.request", "test-service")
				result := rs.apply(span)
				assert.True(result)
				assert.Equal(1.0, span.Metrics["_dd.rule_psr"])
				assert.Equal(0.5, span.Metrics["_dd.limit_psr"])
			})
		}
	})

	t.Run("default-rate", func(t *testing.T) {
		ruleSets := [][]SamplingRule{
			{},
			{ServiceRule("other-service", 0.0)},
		}
		for _, v := range ruleSets {
			t.Run("", func(t *testing.T) {
				assert := assert.New(t)
				os.Setenv("DD_TRACE_SAMPLE_RATE", "0.8")
				defer os.Unsetenv("DD_TRACE_SAMPLE_RATE")
				rs := newRulesSampler(v)

				span := makeSpan("http.request", "test-service")
				result := rs.apply(span)
				assert.True(result)
				assert.Equal(0.8, span.Metrics["_dd.rule_psr"])
				assert.Equal(0.5, span.Metrics["_dd.limit_psr"])
			})
		}
	})
}

func TestRulesSamplerInternals(t *testing.T) {
	makeSpanAt := func(op string, svc string, ts time.Time) *span {
		s := newSpan(op, svc, "", 0, 0, 0)
		s.Start = ts.UnixNano()
		return s
	}

	t.Run("zero-rate", func(t *testing.T) {
		assert := assert.New(t)
		ts := time.Now()
		rs := &rulesSampler{}
		span := makeSpanAt("http.request", "test-service", ts)
		rs.applyRate(span, 0.0, ts)
		assert.Equal(0.0, span.Metrics["_dd.rule_psr"])
		_, ok := span.Metrics["_dd.limit_psr"]
		assert.False(ok)
	})

	t.Run("full-rate", func(t *testing.T) {
		assert := assert.New(t)
		ts := time.Now()
		rs := &rulesSampler{
			ts:           ts.Truncate(time.Second).Add(-1 * time.Second),
			previousRate: 1.0,
			allowed:      1,
			total:        1,
		}
		span := makeSpanAt("http.request", "test-service", ts)
		rs.applyRate(span, 1.0, ts)
		assert.Equal(1.0, span.Metrics["_dd.rule_psr"])
		assert.Equal(1.0, span.Metrics["_dd.limit_psr"])
	})

	t.Run("limited-rate", func(t *testing.T) {
		assert := assert.New(t)
		ts := time.Now()
		rs := &rulesSampler{
			limiter:      rate.NewLimiter(rate.Limit(1.0), 1),
			ts:           ts.Truncate(time.Second).Add(-1 * time.Second),
			previousRate: 1.0,
			allowed:      1,
			total:        1,
		}
		// first span kept, second dropped
		span := makeSpanAt("http.request", "test-service", ts)
		rs.applyRate(span, 1.0, ts)
		assert.EqualValues(ext.PriorityAutoKeep, span.Metrics[keySamplingPriority])
		assert.Equal(1.0, span.Metrics["_dd.rule_psr"])
		assert.Equal(1.0, span.Metrics["_dd.limit_psr"])
		span = makeSpanAt("http.request", "test-service", ts)
		rs.applyRate(span, 1.0, ts)
		assert.EqualValues(ext.PriorityAutoReject, span.Metrics[keySamplingPriority])
		assert.Equal(1.0, span.Metrics["_dd.rule_psr"])
		assert.Equal(0.75, span.Metrics["_dd.limit_psr"])
	})
}

func BenchmarkRulesSampler(b *testing.B) {
	const batchSize = 500
	newTracer := func(opts ...StartOption) *tracer {
		c := new(config)
		defaults(c)
		for _, fn := range opts {
			fn(c)
		}
		return &tracer{
			config:           c,
			payloadChan:      make(chan []*span, batchSize),
			flushChan:        make(chan chan<- struct{}, 1),
			stopped:          make(chan struct{}),
			exitChan:         make(chan struct{}, 1),
			rulesSampling:    newRulesSampler(c.samplingRules),
			prioritySampling: newPrioritySampler(),
		}
	}

	benchmarkStartSpan := func(b *testing.B, t *tracer) {
		internal.SetGlobalTracer(t)
		defer func() {
			close(t.stopped)
			internal.SetGlobalTracer(&internal.NoopTracer{})
		}()
		t.prioritySampling.readRatesJSON(ioutil.NopCloser(strings.NewReader(
			`{
                                        "rate_by_service":{
                                                "service:obfuscate.http,env:":0.5,
                                                "service:obfuscate.http,env:none":0.5
                                        }
                                }`,
		)),
		)
		spans := make([]Span, batchSize)
		b.StopTimer()
		b.ResetTimer()
		for i := 0; i < b.N; i += batchSize {
			n := batchSize
			if i+batchSize > b.N {
				n = b.N - i
			}
			b.StartTimer()
			for j := 0; j < n; j++ {
				spans[j] = t.StartSpan("web.request")
			}
			b.StopTimer()
			for j := 0; j < n; j++ {
				spans[j].Finish()
			}
			d := 0
			for len(t.payloadChan) > 0 {
				<-t.payloadChan
				d++
			}
		}
	}

	b.Run("no-rules", func(b *testing.B) {
		tracer := newTracer()
		benchmarkStartSpan(b, tracer)
	})

	b.Run("unmatching-rules", func(b *testing.B) {
		rules := []SamplingRule{
			ServiceRule("test-service", 1.0),
			ServiceOperationRule("postgres.db", "db.query", 1.0),
			OperationRule("notweb.request", 1.0),
		}
		tracer := newTracer(WithSamplingRules(rules))
		benchmarkStartSpan(b, tracer)
	})

	b.Run("matching-rules", func(b *testing.B) {
		rules := []SamplingRule{
			ServiceRule("test-service", 1.0),
			ServiceOperationRule("postgres.db", "db.query", 1.0),
			OperationRule("web.request", 1.0),
		}
		tracer := newTracer(WithSamplingRules(rules))
		benchmarkStartSpan(b, tracer)
	})

	b.Run("mega-rules", func(b *testing.B) {
		rules := []SamplingRule{
			ServiceRule("test-service", 1.0),
			ServiceOperationRule("postgres.db", "db.query", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("notweb.request", 1.0),
			OperationRule("web.request", 1.0),
		}
		tracer := newTracer(WithSamplingRules(rules))
		benchmarkStartSpan(b, tracer)
	})
}
