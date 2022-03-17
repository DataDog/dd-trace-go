package comment

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestOnQuery(t *testing.T) {
	testCases := []struct {
		name      string
		query     string
		tags      map[string]string
		commented string
	}{
		{
			name:      "query with tag list",
			query:     "SELECT * from FOO",
			tags:      map[string]string{"service": "mine", "operation": "checkout"},
			commented: "SELECT * from FOO /* operation='checkout',service='mine' */",
		},
		{
			name:      "empty query",
			query:     "",
			tags:      map[string]string{"service": "mine", "operation": "elmer's glue"},
			commented: "",
		},
		{
			name:      "query with existing comment",
			query:     "SELECT * from FOO -- test query",
			tags:      map[string]string{"service": "mine", "operation": "elmer's glue"},
			commented: "SELECT * from FOO -- test query /* operation='elmer%27s%20glue',service='mine' */",
		},
		{
			name:      "no tags",
			query:     "SELECT * from FOO",
			tags:      map[string]string{},
			commented: "SELECT * from FOO",
		},
		{
			name:      "nil tags",
			query:     "SELECT * from FOO",
			tags:      nil,
			commented: "SELECT * from FOO",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			commented := OnQuery(tc.query, tc.tags)
			assert.Equal(t, tc.commented, commented)
		})
	}
}

func TestWithTags(t *testing.T) {
	testCases := []struct {
		name    string
		tags    map[string]string
		comment string
	}{
		{
			name:    "simple tag",
			tags:    map[string]string{"service": "mine"},
			comment: "/* service='mine' */",
		},
		{
			name:    "tag list",
			tags:    map[string]string{"service": "mine", "operation": "checkout"},
			comment: "/* operation='checkout',service='mine' */",
		},
		{
			name:    "tag value with single quote",
			tags:    map[string]string{"service": "mine", "operation": "elmer's glue"},
			comment: "/* operation='elmer%27s%20glue',service='mine' */",
		},
		{
			name:    "tag key with space",
			tags:    map[string]string{"service name": "mine"},
			comment: "/* service%20name='mine' */",
		},
		{
			name:    "no tags",
			tags:    map[string]string{},
			comment: "",
		},
		{
			name:    "nil tags",
			tags:    nil,
			comment: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			comment := WithTags(tc.tags)
			assert.Equal(t, tc.comment, comment)
		})
	}
}
