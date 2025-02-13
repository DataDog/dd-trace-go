// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"regexp"
	"sync"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

	"github.com/stretchr/testify/assert"
)

func TestRateSampler(t *testing.T) {
	assert := assert.New(t)
	assert.True(NewAllSampler().Sample(newBasicSpan("test")))
	assert.True(NewRateSampler(1).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0.99).Sample(internal.NoopSpan{}))
}

func TestRateSamplerSetting(t *testing.T) {
	assert := assert.New(t)
	rs := NewRateSampler(1)
	assert.Equal(1.0, rs.Rate())
	rs.SetRate(0.5)
	assert.Equal(0.5, rs.Rate())
}

func TestRulesSamplerConcurrency(_ *testing.T) {
	rules := []SamplingRule{
		ServiceRule("test-service", 1.0),
		NameServiceRule("db.query", "postgres.db", 1.0),
		NameRule("notweb.request", 1.0),
	}
	tracer := newTracer(WithSamplingRules(rules))
	defer tracer.Stop()
	span := func(wg *sync.WaitGroup) {
		defer wg.Done()
		tracer.StartSpan("db.query", ServiceName("postgres.db")).Finish()
	}

	wg := &sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go span(wg)
	}
	wg.Wait()
}

func TestGlobMatch(t *testing.T) {
	for i, tt := range []struct {
		pattern     string
		input       string
		shouldMatch bool
	}{
		// pattern with *
		{"test*", "test", true},
		{"test*", "test-case", true},
		{"test*", "a-test", false},
		{"*test", "a-test", true},
		{"a*case", "acase", true},
		{"a*case", "a-test-case", true},
		{"a*test*case", "a-test-case", true},
		{"a*test*case", "atestcase", true},
		{"a*test*case", "abadcase", false},
		{"*a*a*a*a*a*a", "aaaaaaaaaaaaaaaaaaaaaaaaaax", false},
		{"*a*a*a*a*a*a", "aaaaaaaarrrrrrraaaraaarararaarararaarararaaa", true},
		// pattern with ?
		{"test?", "test", false},
		{"test?", "test-case", false},
		{"test?", "a-test", false},
		{"?test", "a-test", false},
		{"a?case", "acase", false},
		{"a?case", "a-case", true},
		{"a?test?case", "a-test-case", true},
		{"a?test?case", "a-test--case", false},
		// pattern with ? and *
		{"?test*", "atest", true},
		{"?test*", "atestcase", true},
		{"?test*", "testcase", false},
		{"?test*", "testcase", false},
		{"test*case", "testcase", true},
		{"a?test*", "a-test-case", true},
		{"a?test*", "atestcase", false},
		{"a*test?", "a-test-", true},
		{"a*test?", "atestcase", false},
		{"a*test?case", "a--test-case", true},
		{"a*test?case", "a--test--case", false},
		{"a?test*case", "a-testing--case", true},
		{"the?test*case", "the-test-cases", false},
		// valid non-glob regex pattern
		{`[a-z]+\\d+`, "abc123", false},
		{`[a-z]+\\d+`, `[a-z]+\\d+`, true},
		{`\\w+`, `\\w+`, true},
		{`\\w+`, `abc123`, false},
		{"*/*", `a/123`, true},
		{`*\/*`, `a\/123`, true},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			rg := globMatch(tt.pattern)
			if tt.shouldMatch {
				assert.Regexp(t, rg, tt.input)
			} else {
				assert.NotRegexp(t, rg, tt.input)
			}
		})
	}
}

func TestSamplingRuleMarshall(t *testing.T) {
	for i, tt := range []struct {
		in  SamplingRule
		out string
	}{
		{ServiceRule("srv.*", 0), `{"service":"srv.*","sample_rate":0}`},
		{NameServiceRule("ops.*", "srv.*", 0), `{"service":"srv.*","name":"ops.*","sample_rate":0}`},
		{NameServiceRule("ops.*", "srv.*", 0.55), `{"service":"srv.*","name":"ops.*","sample_rate":0.55}`},
		{TagsResourceRule(nil, "http_get", "", "", 0.55), `{"resource":"http_get","sample_rate":0.55}`},
		{TagsResourceRule(map[string]string{"host": "hn-*"}, "http_get", "", "", 0.35), `{"resource":"http_get","sample_rate":0.35,"tags":{"host":"hn-*"}}`},
		{SpanNameServiceRule("ops.*", "srv.*", 0.55), `{"service":"srv.*","name":"ops.*","sample_rate":0.55}`},
		{SpanNameServiceMPSRule("ops.*", "srv.*", 0.55, 1000), `{"service":"srv.*","name":"ops.*","sample_rate":0.55,"max_per_second":1000}`},
		{TagsResourceRule(nil, "//bar", "", "", 1), `{"resource":"//bar","sample_rate":1}`},
		{TagsResourceRule(map[string]string{"tag_key": "tag_value.*"}, "//bar", "", "", 1), `{"resource":"//bar","sample_rate":1,"tags":{"tag_key":"tag_value.*"}}`},
	} {
		m, err := tt.in.MarshalJSON()
		assert.Nil(t, err)
		assert.Equal(t, tt.out, string(m), "at %d index", i)
	}
}

func TestSamplingRuleMarshallGlob(t *testing.T) {
	for i, tt := range []struct {
		pattern string
		input   string
		rgx     *regexp.Regexp
		marshal string
	}{
		// pattern with *
		{"test*", "test", regexp.MustCompile("(?i)^test.*$"), `{"service":"test*","sample_rate":1}`},
		{"*test", "a-test", regexp.MustCompile("(?i)^.*test$"), `{"service":"*test","sample_rate":1}`},
		{"a*case", "acase", regexp.MustCompile("(?i)^a.*case$"), `{"service":"a*case","sample_rate":1}`},
		// pattern regexp.MustCompile(), ``, with ?
		{"a?case", "a-case", regexp.MustCompile("(?i)^a.case$"), `{"service":"a?case","sample_rate":1}`},
		{"a?test?case", "a-test-case", regexp.MustCompile("(?i)^a.test.case$"), `{"service":"a?test?case","sample_rate":1}`},
		//// pattern with ? regexp.MustCompile(), ``, and *
		{"?test*", "atest", regexp.MustCompile("(?i)^.test.*$"), `{"service":"?test*","sample_rate":1}`},
		{"test*case", "testcase", regexp.MustCompile("(?i)^test.*case$"), `{"service":"test*case","sample_rate":1}`},
		{"a?test*", "a-test-case", regexp.MustCompile("(?i)^a.test.*$"), `{"service":"a?test*","sample_rate":1}`},
		{"a*test?", "a-test-", regexp.MustCompile("(?i)^a.*test.$"), `{"service":"a*test?","sample_rate":1}`},
		{"a*test?case", "a--test-case", regexp.MustCompile("(?i)^a.*test.case$"), `{"service":"a*test?case","sample_rate":1}`},
		{"a?test*case", "a-testing--case", regexp.MustCompile("(?i)^a.test.*case$"), `{"service":"a?test*case","sample_rate":1}`},
		//// valid non-glob regex regexp.MustCompile(), ``, pattern
		{"*/*", `a/123`, regexp.MustCompile("(?i)^.*/.*$"), `{"service":"*/*","sample_rate":1}`},
		{`*\/*`, `a\/123`, regexp.MustCompile("(?i)^.*/.*$"), `{"service":"*/*","sample_rate":1}`},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			// the goal of this test is
			// 1. to verify that the glob pattern is correctly converted to a regex
			// 2. to verify that the rule is correctly marshalled

			rules, _ := unmarshalSamplingRules([]byte(fmt.Sprintf(`[{"service": "%s", "sample_rate": 1.0}]`, tt.pattern)),
				SamplingRuleTrace)
			rule := rules[0]

			assert.Regexp(t, rules[0].Service, tt.input)
			assert.Equal(t, tt.rgx.String(), rule.Service.String())

			m, err := rule.MarshalJSON()
			assert.Nil(t, err)
			assert.Equal(t, tt.marshal, string(m))
		})
	}
}
