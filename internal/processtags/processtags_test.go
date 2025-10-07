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
