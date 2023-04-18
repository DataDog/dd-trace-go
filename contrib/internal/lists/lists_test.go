// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package lists

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCombinations(t *testing.T) {
	{
		combos := Combinations([]string{"cat", "dog", "bird", "mouse"}, 3)
		assert.Equal(t, [][]string{
			{"cat", "dog", "bird"},
			{"cat", "dog", "mouse"},
			{"cat", "bird", "mouse"},
			{"dog", "bird", "mouse"},
		}, combos)
	}
	{
		combos := Combinations([]string{"cat", "dog", "bird", "mouse"}, 2)
		assert.Equal(t, [][]string{
			{"cat", "dog"},
			{"cat", "bird"},
			{"cat", "mouse"},
			{"dog", "bird"},
			{"dog", "mouse"},
			{"bird", "mouse"},
		}, combos)
	}
	{
		combos := Combinations([]string{"cat", "dog", "bird", "mouse"}, 1)
		assert.Equal(t, [][]string{
			{"cat"},
			{"dog"},
			{"bird"},
			{"mouse"},
		}, combos)
	}
}

func TestConcatStringSlices(t *testing.T) {
	sl := ConcatStringSlices([]string{"a", "b"}, []string{}, []string{"c", "d"})
	assert.Equal(t, []string{"a", "b", "c", "d"}, sl)
}

func TestRepeatedStringSlice(t *testing.T) {
	sl := RepeatedStringSlice("abc", 3)
	assert.Equal(t, []string{"abc", "abc", "abc"}, sl)
}
