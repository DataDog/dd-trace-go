package tracer

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegexEqualFalseNegative(t *testing.T) {
	tests := []struct {
		name          string
		regex_1       *regexp.Regexp
		regex_2       *regexp.Regexp
		expectedEqual bool
	}{
		{
			name:          "nil regex equals nil regex",
			regex_1:       nil,
			regex_2:       nil,
			expectedEqual: true,
		},
		{
			name:          "nil regex not equal non-nil regex",
			regex_1:       nil,
			regex_2:       regexp.MustCompile("abc"),
			expectedEqual: false,
		},
		{
			name:          "regex with same strings",
			regex_1:       regexp.MustCompile("abc.*"),
			regex_2:       regexp.MustCompile("abc.*"),
			expectedEqual: true,
		},
		{
			name:          "not equal regex with wildcards",
			regex_1:       regexp.MustCompile("abc.*"),
			regex_2:       regexp.MustCompile("abc.*abc"),
			expectedEqual: false,
		},
		{
			name:          "same regex but false negatives",
			regex_1:       regexp.MustCompile("(a+b*)*"),
			regex_2:       regexp.MustCompile("(a+b)*"),
			expectedEqual: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expectedEqual, regexEqualsFalseNegative(test.regex_1, test.regex_2))
		})

	}
}

func TestSamplingRuleEquals(t *testing.T) {
	tests := []struct {
		name          string
		rule_1        string
		rule_2        string
		expectedEqual bool
	}{
		{
			name:          "exact same rules",
			rule_1:        `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule_2:        `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			expectedEqual: true,
		},
		{
			name:          "different resources",
			rule_1:        `{"service":"test-serv","resource":"resource-*-abc","name":"op-name","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule_2:        `{"service":"test-serv","resource":"resource-*","name":"op-name","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			expectedEqual: false,
		},
		{
			name:          "different names",
			rule_1:        `{"service":"test-serv","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule_2:        `{"service":"test-serv","resource":"resource-*-abc","name":"op-name","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			expectedEqual: false,
		},
		{
			name:          "different tags",
			rule_1:        `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule_2:        `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??","tag-b":"tv-b"},"sample_rate":0.1}`,
			expectedEqual: false,
		},
		{
			name:          "different rates",
			rule_1:        `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule_2:        `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.2}`,
			expectedEqual: false,
		},
		{
			name:          "same rules false negatives",
			rule_1:        `{"service":"test-*","resource":"resource-*","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule_2:        `{"service":"test-*","resource":"resource-**","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			expectedEqual: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var rule_1, rule_2 SamplingRule
			assert.NoError(t, json.Unmarshal([]byte(test.rule_1), &rule_1))
			assert.NoError(t, json.Unmarshal([]byte(test.rule_2), &rule_2))
			assert.False(t, rule_1.Equals(nil))
			assert.Equal(t, test.expectedEqual, rule_1.Equals(&rule_2))
		})
	}
}

func TestSamplingRuleNilSlicesEqual(t *testing.T) {
	assert.True(t, Equals(nil, nil))
	{
		var rules []SamplingRule
		assert.NoError(t, json.Unmarshal([]byte(`[{"service":"abc"}]`), &rules))
		assert.False(t, Equals(nil, rules))
	}
	{
		var rules []SamplingRule
		assert.NoError(t, json.Unmarshal([]byte(`[{"service":"abc"}]`), &rules))
		assert.False(t, Equals(rules, nil))
	}
}

func TestSamplingRuleSlicesEqual(t *testing.T) {
	tests := []struct {
		name          string
		ruleset_1     string
		ruleset_2     string
		expectedEqual bool
	}{
		{
			name:          "empty rulesets",
			ruleset_1:     "[]",
			ruleset_2:     "[]",
			expectedEqual: true,
		},
		{
			name:          "one empty another not",
			ruleset_1:     "[]",
			ruleset_2:     `[{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			expectedEqual: false,
		},
		{
			name:          "same rules",
			ruleset_1:     `[{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			ruleset_2:     `[{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			expectedEqual: true,
		},
		{
			name:          "different rules",
			ruleset_1:     `[{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			ruleset_2:     `[{"service":"test-*","resource":"resource-*","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			expectedEqual: false,
		},
		{
			name:      "one has extra rules",
			ruleset_1: `[{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			ruleset_2: `[
				{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1},
				{"service":"test-*","resource":"abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}
			]`,
			expectedEqual: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var ruleset_1, ruleset_2 []SamplingRule
			assert.NoError(t, json.Unmarshal([]byte(test.ruleset_1), &ruleset_1))
			assert.NoError(t, json.Unmarshal([]byte(test.ruleset_2), &ruleset_2))
			assert.Equal(t, test.expectedEqual, Equals(ruleset_1, ruleset_2))
		})
	}
}
