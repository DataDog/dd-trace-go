// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpanEnrichment_GetSpanTags(t *testing.T) {
	se := NewSpanEnrichment()

	sid42 := uint32(42)
	sid101 := uint32(101)
	se.AddEvaluation(&FeatureFlagEvaluation{FlagKey: "experiment-b", SerialID: &sid101})
	se.AddEvaluation(&FeatureFlagEvaluation{FlagKey: "experiment-a", SerialID: &sid42, Subject: "user-123"})
	se.AddEvaluation(&FeatureFlagEvaluation{FlagKey: "experiment-a-dup", SerialID: &sid42, Subject: "user-123"})
	se.AddEvaluation(&FeatureFlagEvaluation{FlagKey: "default-flag", DefaultValue: "default-val"})

	tags := se.GetSpanTags()
	assert.Equal(t, []uint32{42, 101}, decodeSerialIDs(t, tags["ffe_flags_enc"]))

	var subjects map[string]string
	require.NoError(t, json.Unmarshal([]byte(tags["ffe_subjects_enc"]), &subjects))
	user123HashBytes := sha256.Sum256([]byte("user-123"))
	user123Hash := hex.EncodeToString(user123HashBytes[:])
	assert.Equal(t, map[string]string{user123Hash: encodeSerialIDs(map[uint32]struct{}{42: {}})}, subjects)

	var defaults map[string]string
	require.NoError(t, json.Unmarshal([]byte(tags["ffe_runtime_defaults"]), &defaults))
	assert.Equal(t, map[string]string{"default-flag": "default-val"}, defaults)
}

func TestSpanEnrichment_OmitEmpty(t *testing.T) {
	se := NewSpanEnrichment()
	tags := se.GetSpanTags()
	assert.Empty(t, tags)
}

func TestSpanEnrichment_Limits(t *testing.T) {
	t.Run("serial ids", func(t *testing.T) {
		se := NewSpanEnrichment()
		for i := range spanEnrichmentMaxSerialIDs + 1 {
			sid := uint32(i + 1)
			se.AddEvaluation(&FeatureFlagEvaluation{FlagKey: fmt.Sprintf("flag-%d", i), SerialID: &sid})
		}

		ids := decodeSerialIDs(t, se.GetSpanTags()["ffe_flags_enc"])
		require.Len(t, ids, spanEnrichmentMaxSerialIDs)
		assert.Equal(t, uint32(spanEnrichmentMaxSerialIDs), ids[len(ids)-1])
	})

	t.Run("subjects", func(t *testing.T) {
		se := NewSpanEnrichment()
		for i := range spanEnrichmentMaxSubjects + 1 {
			sid := uint32(i + 1)
			se.AddEvaluation(&FeatureFlagEvaluation{
				FlagKey:  fmt.Sprintf("flag-%d", i),
				SerialID: &sid,
				Subject:  fmt.Sprintf("user-%d", i),
			})
		}

		var subjects map[string]string
		require.NoError(t, json.Unmarshal([]byte(se.GetSpanTags()["ffe_subjects_enc"]), &subjects))
		require.Len(t, subjects, spanEnrichmentMaxSubjects)
	})

	t.Run("runtime defaults", func(t *testing.T) {
		se := NewSpanEnrichment()
		for i := range spanEnrichmentMaxRuntimeDefaults + 1 {
			se.AddEvaluation(&FeatureFlagEvaluation{FlagKey: fmt.Sprintf("flag-%d", i), DefaultValue: []int{i}})
		}

		var defaults map[string]string
		require.NoError(t, json.Unmarshal([]byte(se.GetSpanTags()["ffe_runtime_defaults"]), &defaults))
		require.Len(t, defaults, spanEnrichmentMaxRuntimeDefaults)
	})
}

func decodeSerialIDs(t *testing.T, enc string) []uint32 {
	t.Helper()

	b, err := base64.StdEncoding.DecodeString(enc)
	require.NoError(t, err)

	ids := make([]uint32, 0)
	var id uint32
	var shift uint
	var diff uint32
	for _, v := range b {
		diff |= uint32(v&0x7f) << shift
		if v&0x80 != 0 {
			shift += 7
			continue
		}
		id += diff
		ids = append(ids, id)
		diff = 0
		shift = 0
	}
	return ids
}
