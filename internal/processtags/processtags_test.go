// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package processtags

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestProcessTagsConcurrentReadWrite races lock-free String()/Slice() readers against rebuild() writers (run under -race).
func TestProcessTagsConcurrentReadWrite(t *testing.T) {
	t.Cleanup(Reload)
	Reload()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for w := range 4 {
		wg.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				SetServiceNameTag("svc-"+strconv.Itoa(w)+"-"+strconv.Itoa(i), i%2 == 0)
			}
		})
	}

	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				tags := GlobalTags()
				_ = len(tags.String())
				for _, s := range tags.Slice() { // dereference header + elements
					_ = len(s)
				}
			}
		})
	}

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

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
		// Reload creates pTags but add is never called, leaving pTags.tags nil.
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

func TestDirectoryTagValue(t *testing.T) {
	t.Run("filters non-informative directory names", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			dir  string
		}{
			{name: "empty", dir: ""},
			{name: "bin", dir: "bin"},
			{name: "root", dir: string(os.PathSeparator)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := tc.dir
				_, ok := directoryTagValue(dir)
				assert.False(t, ok)
			})
		}
	})

	t.Run("keeps informative directory names", func(t *testing.T) {
		got, ok := directoryTagValue(filepath.Join("usr", "local", "app"))
		assert.True(t, ok)
		assert.Equal(t, "app", got)
	})
}

func TestContainerTagsHash(t *testing.T) {
	t.Cleanup(func() { SetContainerTagsHash("") })

	SetContainerTagsHash("hash-1")
	assert.Equal(t, "hash-1", ContainerTagsHash())

	SetContainerTagsHash("hash-2")
	assert.Equal(t, "hash-2", ContainerTagsHash())

	SetContainerTagsHash("")
	assert.Empty(t, ContainerTagsHash())
}
