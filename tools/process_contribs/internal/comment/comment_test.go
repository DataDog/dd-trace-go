package comment_test

import (
	"github.com/DataDog/dd-trace-go/v2/tools/process_contribs/internal/comment"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestParseComment(t *testing.T) {
	testCases := []struct {
		name   string
		input  string
		want   comment.Comment
		wantOk bool
	}{
		{
			name:  "parameters",
			input: "ddtrace:entrypoint:wrap-custom-type skip-methods:WithContext",
			want: comment.Comment{
				Command:   "wrap-custom-type",
				Arguments: map[string]string{"skip-methods": "WithContext", "__raw_args": "skip-methods:WithContext"},
			},
			wantOk: true,
		},
		{
			name:  "parameters_with_colon",
			input: `ddtrace:entrypoint:wrap-custom-type config:"value:with:colons" another:arg`,
			want: comment.Comment{
				Command:   "wrap-custom-type",
				Arguments: map[string]string{"config": "value:with:colons", "another": "arg", "__raw_args": "config:\"value:with:colons\" another:arg"},
			},
			wantOk: true,
		},
		{
			name:  "parameters_with_space",
			input: `ddtrace:entrypoint:test arg1:"a value with spaces" key2:value2`,
			want: comment.Comment{
				Command:   "test",
				Arguments: map[string]string{"arg1": "a value with spaces", "key2": "value2", "__raw_args": "arg1:\"a value with spaces\" key2:value2"},
			},
			wantOk: true,
		},
		{
			name:   "invalid_format",
			input:  "invalid format",
			want:   comment.Comment{},
			wantOk: false,
		},
		{
			name:  "no_arguments",
			input: "ddtrace:entrypoint:wrap-custom-type",
			want: comment.Comment{
				Command:   "wrap-custom-type",
				Arguments: map[string]string{"__raw_args": ""},
			},
			wantOk: true,
		},
		{
			name:  "with_comment_prefix_and_args",
			input: "//ddtrace:entrypoint:wrap-custom-type skip-methods:WithContext",
			want: comment.Comment{
				Command:   "wrap-custom-type",
				Arguments: map[string]string{"skip-methods": "WithContext", "__raw_args": "skip-methods:WithContext"},
			},
			wantOk: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, ok := comment.ParseComment(tc.input)
			assert.Equal(t, tc.wantOk, ok)
			assert.Equal(t, tc.want, result)
		})
	}
}
