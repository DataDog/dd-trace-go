// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package processtags

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetServiceNameTag(t *testing.T) {
	t.Run("auto-assigned sets svc.auto", func(t *testing.T) {
		t.Cleanup(Reload)
		Reload()
		SetServiceNameTag("myapp", false)
		tags := GlobalTags()
		assert.Contains(t, tags.String(), "svc.auto:myapp")
		assert.NotContains(t, tags.String(), "svc.user")
		assert.Contains(t, tags.Slice(), "svc.auto:myapp")
	})

	t.Run("user-defined sets svc.user", func(t *testing.T) {
		t.Cleanup(Reload)
		Reload()
		SetServiceNameTag("myapp", true)
		tags := GlobalTags()
		assert.Contains(t, tags.String(), "svc.user:true")
		assert.NotContains(t, tags.String(), "svc.auto")
		assert.Contains(t, tags.Slice(), "svc.user:true")
	})

	t.Run("switching from auto to user removes svc.auto", func(t *testing.T) {
		t.Cleanup(Reload)
		Reload()
		SetServiceNameTag("myapp", false)
		SetServiceNameTag("myapp", true)
		tags := GlobalTags()
		assert.Contains(t, tags.String(), "svc.user:true")
		assert.NotContains(t, tags.String(), "svc.auto")
	})

	t.Run("switching from user to auto removes svc.user", func(t *testing.T) {
		t.Cleanup(Reload)
		Reload()
		SetServiceNameTag("myapp", true)
		SetServiceNameTag("otherapp", false)
		tags := GlobalTags()
		assert.Contains(t, tags.String(), "svc.auto:otherapp")
		assert.NotContains(t, tags.String(), "svc.user")
	})

	t.Run("works when tags map not yet initialised", func(t *testing.T) {
		t.Cleanup(Reload)
		// Simulate collect() returning empty (e.g. os.Executable fails):
		// Reload creates pTags but Add is never called, leaving pTags.tags nil.
		pTags = &ProcessTags{}
		SetServiceNameTag("myapp", false)
		tags := GlobalTags()
		assert.Contains(t, tags.String(), "svc.auto:myapp")
	})

	t.Run("no-op when disabled", func(t *testing.T) {
		t.Cleanup(Reload) // register before t.Setenv so it runs after env is restored
		t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "false")
		Reload()
		SetServiceNameTag("myapp", false)
		assert.Nil(t, GlobalTags())
	})
}

func TestProcessTags(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		wantTagsRe := regexp.MustCompile(`^entrypoint\.basedir:[a-zA-Z0-9._-]+,entrypoint\.name:[a-zA-Z0-9._-]+,entrypoint.type:executable,entrypoint\.workdir:[a-zA-Z0-9._-]+$`)
		p := GlobalTags()
		assert.NotNil(t, p)
		assert.NotEmpty(t, p.String())
		assert.Regexp(t, wantTagsRe, p.String(), "wrong string serialized tags")

		assert.NotEmpty(t, p.Slice())
		assert.Regexp(t, wantTagsRe, strings.Join(p.Slice(), ","), "wrong slice serialized tags")
	})

	t.Run("disabled", func(t *testing.T) {
		t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "false")
		Reload()

		p := GlobalTags()
		assert.Nil(t, p)
		assert.Empty(t, p.String())
		assert.Empty(t, p.Slice())
	})
}

func TestIsValidTagValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "empty string", value: "", want: false},
		{name: "forward slash", value: "/", want: false},
		{name: "backslash", value: "\\", want: false},
		{name: "bin", value: "bin", want: false},
		{name: "dot", value: ".", want: true},
		{name: "valid app name", value: "myapp", want: true},
		{name: "valid usr", value: "usr", want: true},
		{name: "valid workspace", value: "workspace", want: true},
		{name: "valid hyphenated name", value: "my-service", want: true},
		{name: "valid dotted name", value: "app.test", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isValidTagValue(tt.value))
		})
	}
}
