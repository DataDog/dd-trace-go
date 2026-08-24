// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License, Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package profiler

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math"

	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"
	"github.com/DataDog/dd-trace-go/v2/profiler/internal/pproflite"
)

var strippedLabelKeyNames = map[string]struct{}{
	traceprof.LocalRootSpanID: {},
}

// stripPPROFLabels ... TODO
func stripPPROFLabels(data []byte, w io.Writer) error {
	decoder := pproflite.NewDecoder(data)
	skippedKeyStringIndices := make(map[int64]struct{})
	n := 0
	if err := decoder.FieldEach(func(field pproflite.Field) error {
		if _, ok := strippedLabelKeyNames[string(field.(*pproflite.StringTable).Value)]; ok {
			skippedKeyStringIndices[int64(n)] = struct{}{}
		}
		n++
		return nil
	}, pproflite.StringTableDecoder); err != nil {
		return err
	}

	if n > math.MaxUint32 {
		return fmt.Errorf("too many strings: %d", n)
	}

	// indices is zero for unreferenced strings and otherwise stores its new
	// index plus one. It is updated in place after strings have been marked.
	indices := make([]uint32, n)
	if len(indices) > 0 {
		indices[0] = 1 // profile.proto requires string_table[0] to be empty.
	}
	mark := func(index int64) { indices[index] = 1 }
	isStrippedLabel := func(label pproflite.Label) bool {
		_, ok := skippedKeyStringIndices[label.Key]
		return ok
	}

	// First find strings used by all fields that will remain in the profile.
	if err := decoder.FieldEach(func(field pproflite.Field) error {
		switch field := field.(type) {
		case *pproflite.SampleType:
			mark(field.ValueType.Type)
			mark(field.ValueType.Unit)
		case *pproflite.Sample:
			for _, label := range field.Label {
				if isStrippedLabel(label) {
					continue
				}
				mark(label.Key)
				mark(label.Str)
				mark(label.NumUnit)
			}
		case *pproflite.Mapping:
			mark(field.Filename)
			mark(field.BuildID)
		case *pproflite.Function:
			mark(field.Name)
			mark(field.SystemName)
			mark(field.FileName)
		case *pproflite.DropFrames:
			mark(field.Value)
		case *pproflite.KeepFrames:
			mark(field.Value)
		case *pproflite.PeriodType:
			mark(field.ValueType.Type)
			mark(field.ValueType.Unit)
		case *pproflite.Comment:
			mark(field.Value)
		case *pproflite.DefaultSampleType:
			mark(field.Value)
		}
		return nil
	}); err != nil {
		return err
	}

	// Build the old-to-new string table index translation in place.
	var next uint32
	for i, index := range indices {
		if index != 0 {
			next++
			indices[i] = next
		}
	}
	translate := func(index *int64) { *index = int64(indices[*index] - 1) }

	encoder := pproflite.NewEncoder(w)
	stringIndex := 0
	return decoder.FieldEach(func(field pproflite.Field) error {
		switch field := field.(type) {
		case *pproflite.StringTable:
			keep := indices[stringIndex] != 0
			stringIndex++
			if !keep {
				return nil
			}
		case *pproflite.SampleType:
			translate(&field.ValueType.Type)
			translate(&field.ValueType.Unit)
		case *pproflite.Sample:
			labels := field.Label[:0]
			for _, label := range field.Label {
				if isStrippedLabel(label) {
					continue
				}
				translate(&label.Key)
				translate(&label.Str)
				translate(&label.NumUnit)
				labels = append(labels, label)
			}
			field.Label = labels
		case *pproflite.Mapping:
			translate(&field.Filename)
			translate(&field.BuildID)
		case *pproflite.Function:
			translate(&field.Name)
			translate(&field.SystemName)
			translate(&field.FileName)
		case *pproflite.DropFrames:
			translate(&field.Value)
		case *pproflite.KeepFrames:
			translate(&field.Value)
		case *pproflite.PeriodType:
			translate(&field.ValueType.Type)
			translate(&field.ValueType.Unit)
		case *pproflite.Comment:
			translate(&field.Value)
		case *pproflite.DefaultSampleType:
			translate(&field.Value)
		}
		return encoder.Encode(field)
	})
}

// stripPPROFLabelsGzip decompresses a runtime CPU profile before stripping
// its labels. The caller's compression pipeline performs the final encoding.
func stripPPROFLabelsGzip(data []byte, output io.Writer) error {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer r.Close()
	profile, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return stripPPROFLabels(profile, output)
}
