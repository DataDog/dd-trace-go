// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package timed

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEntryData(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		var subject entryData

		require.EqualValues(t, 0, subject.AccessTime())
		require.EqualValues(t, 0, subject.SampleTime())
	})

	t.Run("newEntryData", func(t *testing.T) {
		atime := rand.Uint32()
		stime := rand.Uint32()
		subject := newEntryData(atime, stime)

		require.Equal(t, atime, subject.AccessTime())
		require.Equal(t, stime, subject.SampleTime())

		t.Run("WithAccessTime", func(t *testing.T) {
			subject = subject.WithAccessTime(atime + 1)
			require.Equal(t, atime+1, subject.AccessTime())
			require.Equal(t, stime, subject.SampleTime()) // Unchanged
		})
	})
}
