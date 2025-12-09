// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gls

import (
	"runtime"
	"sync"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//dd:orchestrion-enabled
const orchestrionEnabled = false

func TestBuiltWithOrchestrion(t *testing.T) {
	require.Equal(t, built.WithOrchestrion, orchestrionEnabled)
}

func TestSimple(t *testing.T) {
	expected := "Hello, World!"

	set(expected)
	actual := get()

	if orchestrionEnabled {
		t.Log("Orchestrion IS enabled")
		require.Equal(t, expected, actual)
	} else {
		t.Log("Orchestrion IS NOT enabled")
		require.Nil(t, actual)
	}
}

// TestCGO tests that the GLS is correctly set even when the code comes from a cgo callback.
func TestCGO(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("Orchestrion is not enabled")
	}

	expected := "I am inside a cgo callback"
	set(nil)
	cgoCall()
	require.Equal(t, expected, get())
}

func TestConcurrency(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("Orchestrion is not enabled")
	}

	nbSets := 5000
	nbGoRoutines := 300

	var wg sync.WaitGroup

	wg.Add(nbGoRoutines)
	for i := 0; i < nbGoRoutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < nbSets; j++ {
				set(j)
				assert.Equal(t, j, get())
			}
		}()
	}

	wg.Wait()
}

func BenchmarkGLS(b *testing.B) {
	if !orchestrionEnabled {
		b.Skip("Orchestrion is not enabled")
	}

	b.Run("Set", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			set(i)
		}
	})

	b.Run("Get", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			runtime.KeepAlive(get())
		}
	})
}
