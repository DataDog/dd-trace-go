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
