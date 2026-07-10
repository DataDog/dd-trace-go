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
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpanEnrichment_GetSpanTags(t *testing.T) {
	se := newSpanEnrichment()

	sid42 := uint32(42)
	sid101 := uint32(101)
	se.addEvaluation(&FeatureFlagEvaluation{FlagKey: "experiment-b", SerialID: &sid101})
	se.addEvaluation(&FeatureFlagEvaluation{FlagKey: "experiment-a", SerialID: &sid42, Subject: "user-123"})
	se.addEvaluation(&FeatureFlagEvaluation{FlagKey: "experiment-a-dup", SerialID: &sid42, Subject: "user-123"})
	se.addEvaluation(&FeatureFlagEvaluation{FlagKey: "default-flag", DefaultValue: "default-val"})

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
	se := newSpanEnrichment()
	tags := se.GetSpanTags()
	assert.Empty(t, tags)
}

func TestSpanEnrichment_Limits(t *testing.T) {
	t.Run("serial ids", func(t *testing.T) {
		se := newSpanEnrichment()
		for i := range spanEnrichmentMaxSerialIDs + 1 {
			sid := uint32(i + 1)
			se.addEvaluation(&FeatureFlagEvaluation{FlagKey: fmt.Sprintf("flag-%d", i), SerialID: &sid})
		}

		ids := decodeSerialIDs(t, se.GetSpanTags()["ffe_flags_enc"])
		require.Len(t, ids, spanEnrichmentMaxSerialIDs)
		assert.Equal(t, uint32(spanEnrichmentMaxSerialIDs), ids[len(ids)-1])
	})

	t.Run("subjects", func(t *testing.T) {
		se := newSpanEnrichment()
		for i := range spanEnrichmentMaxSubjects + 1 {
			sid := uint32(i + 1)
			se.addEvaluation(&FeatureFlagEvaluation{
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
		se := newSpanEnrichment()
		for i := range spanEnrichmentMaxRuntimeDefaults + 1 {
			se.addEvaluation(&FeatureFlagEvaluation{FlagKey: fmt.Sprintf("flag-%d", i), DefaultValue: []int{i}})
		}

		var defaults map[string]string
		require.NoError(t, json.Unmarshal([]byte(se.GetSpanTags()["ffe_runtime_defaults"]), &defaults))
		require.Len(t, defaults, spanEnrichmentMaxRuntimeDefaults)
	})

	t.Run("serial ids per subject", func(t *testing.T) {
		se := newSpanEnrichment()
		for i := range spanEnrichmentMaxSerialIDsPerSubject + 1 {
			sid := uint32(i + 1)
			se.addEvaluation(&FeatureFlagEvaluation{
				FlagKey:  fmt.Sprintf("flag-%d", i),
				SerialID: &sid,
				Subject:  "user-1",
			})
		}

		var subjects map[string]string
		require.NoError(t, json.Unmarshal([]byte(se.GetSpanTags()["ffe_subjects_enc"]), &subjects))
		require.Len(t, subjects, 1)
		user1HashBytes := sha256.Sum256([]byte("user-1"))
		user1Hash := hex.EncodeToString(user1HashBytes[:])
		ids := decodeSerialIDs(t, subjects[user1Hash])
		assert.Len(t, ids, spanEnrichmentMaxSerialIDsPerSubject)
	})
}

func TestSpanEnrichment_NonStringRuntimeDefault(t *testing.T) {
	se := newSpanEnrichment()
	se.addEvaluation(&FeatureFlagEvaluation{FlagKey: "int-slice-flag", DefaultValue: []int{1, 2, 3}})

	var defaults map[string]string
	require.NoError(t, json.Unmarshal([]byte(se.GetSpanTags()["ffe_runtime_defaults"]), &defaults))
	assert.Equal(t, "[1,2,3]", defaults["int-slice-flag"])
}

func TestSpanEnrichment_TruncateRuntimeDefault(t *testing.T) {
	se := newSpanEnrichment()
	longVal := strings.Repeat("a", spanEnrichmentMaxDefaultValueLen+10)
	se.addEvaluation(&FeatureFlagEvaluation{FlagKey: "long-flag", DefaultValue: longVal})

	var defaults map[string]string
	require.NoError(t, json.Unmarshal([]byte(se.GetSpanTags()["ffe_runtime_defaults"]), &defaults))
	got := defaults["long-flag"]
	assert.Len(t, got, spanEnrichmentMaxDefaultValueLen)
	assert.Equal(t, strings.Repeat("a", spanEnrichmentMaxDefaultValueLen), got)
}

// TestSpanEnrichment_TruncateRuntimeDefault_UTF8 verifies that truncation never
// produces invalid UTF-8 when the cut point falls inside a multi-byte sequence.
func TestSpanEnrichment_TruncateRuntimeDefault_UTF8(t *testing.T) {
	// Build a string whose multi-byte character (€ = 3 bytes: \xe2\x82\xac)
	// straddles the truncation boundary: 62 ASCII bytes + "€" = 65 bytes total.
	// Byte-slicing at spanEnrichmentMaxDefaultValueLen (64) would leave a
	// 2-byte incomplete sequence; ToValidUTF8 must strip it.
	val := strings.Repeat("a", spanEnrichmentMaxDefaultValueLen-2) + "€" // 62 + 3 = 65 bytes

	se := newSpanEnrichment()
	se.addEvaluation(&FeatureFlagEvaluation{FlagKey: "utf8-flag", DefaultValue: val})

	var defaults map[string]string
	require.NoError(t, json.Unmarshal([]byte(se.GetSpanTags()["ffe_runtime_defaults"]), &defaults))
	got := defaults["utf8-flag"]
	assert.True(t, utf8.ValidString(got), "stored value must be valid UTF-8")
	// Incomplete \xe2\x82 tail is dropped, only the 62 ASCII bytes survive.
	assert.Equal(t, strings.Repeat("a", spanEnrichmentMaxDefaultValueLen-2), got)
}

// TestSpanEnrichment_InvalidUTF8_NoTruncation verifies that invalid UTF-8 bytes
// are sanitized even when the value is within the length limit.
func TestSpanEnrichment_InvalidUTF8_NoTruncation(t *testing.T) {
	invalidUTF8 := "hello\xff\xfeworld" // \xff and \xfe are not valid UTF-8

	se := newSpanEnrichment()
	se.addEvaluation(&FeatureFlagEvaluation{FlagKey: "bad-utf8-flag", DefaultValue: invalidUTF8})

	var defaults map[string]string
	require.NoError(t, json.Unmarshal([]byte(se.GetSpanTags()["ffe_runtime_defaults"]), &defaults))
	got := defaults["bad-utf8-flag"]
	assert.True(t, utf8.ValidString(got), "stored value must be valid UTF-8")
	assert.Equal(t, "helloworld", got)
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
