// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package genaitrace

import (
	"encoding/json"
	"strconv"
	"strings"

	"google.golang.org/genai"
)

// streamAccumulator coalesces streamed GenerateContentResponse chunks into a
// single synthetic response so the span can be annotated with the full text,
// final usage metadata and finish reason.
type streamAccumulator struct {
	texts        map[int32]*strings.Builder
	candidates   map[int32]*genai.Candidate
	indexOrder   []int32
	usage        *genai.GenerateContentResponseUsageMetadata
	modelVersion string
	responseID   string
}

func newStreamAccumulator() *streamAccumulator {
	return &streamAccumulator{
		texts:      map[int32]*strings.Builder{},
		candidates: map[int32]*genai.Candidate{},
	}
}

func (s *streamAccumulator) add(chunk *genai.GenerateContentResponse) {
	if chunk == nil {
		return
	}
	if chunk.UsageMetadata != nil {
		s.usage = chunk.UsageMetadata
	}
	if chunk.ModelVersion != "" {
		s.modelVersion = chunk.ModelVersion
	}
	if chunk.ResponseID != "" {
		s.responseID = chunk.ResponseID
	}
	for _, cand := range chunk.Candidates {
		if cand == nil {
			continue
		}
		idx := cand.Index
		if _, seen := s.candidates[idx]; !seen {
			s.indexOrder = append(s.indexOrder, idx)
			s.texts[idx] = &strings.Builder{}
		}
		// Keep the latest candidate to capture finish reason / safety ratings.
		s.candidates[idx] = cand
		if cand.Content != nil {
			for _, p := range cand.Content.Parts {
				if p != nil && p.Text != "" {
					s.texts[idx].WriteString(p.Text)
				}
			}
		}
	}
}

func (s *streamAccumulator) response() *genai.GenerateContentResponse {
	if len(s.indexOrder) == 0 && s.usage == nil {
		return nil
	}
	resp := &genai.GenerateContentResponse{
		UsageMetadata: s.usage,
		ModelVersion:  s.modelVersion,
		ResponseID:    s.responseID,
	}
	for _, idx := range s.indexOrder {
		cand := s.candidates[idx]
		role := string(genai.RoleModel)
		if cand.Content != nil && cand.Content.Role != "" {
			role = cand.Content.Role
		}
		merged := &genai.Candidate{
			FinishReason:  cand.FinishReason,
			FinishMessage: cand.FinishMessage,
			Index:         cand.Index,
			Content: &genai.Content{
				Role:  role,
				Parts: []*genai.Part{{Text: s.texts[idx].String()}},
			},
		}
		resp.Candidates = append(resp.Candidates, merged)
	}
	return resp
}

// jsonRaw marshals v to json.RawMessage; on error it returns nil.
func jsonRaw(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
