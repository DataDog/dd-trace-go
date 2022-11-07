// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package fastdelta

import (
	"fmt"
	"io"

	"github.com/spaolacci/murmur3"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/pproflite"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/pprofutils"
)

/*
# Outline

The end goal is to match up samples between the two profiles and take their
difference. A sample is a unique (call stack, labels) pair with an associated
sequence of values, where "call stack" refers to a sequence of program
counters/instruction addresses, and labels are key/value pairs associated with a
stack (so we can have the same call stack appear in two different samples if the
labels are different)

# Implementation

Computing the delta profile takes four passes over the input:

Pass 1 (gathering information about location IDs):
	* Build a mapping of location IDs to instruction addresses,
	  mappings, and function IDs.
	* Build the string table, so we can resolve label keys and values
	* Find the value types by name, so we know which sample values to
	  compute differences for

Pass 2 (Computing deltas for each sample):
	* Create a new buffer where we'll write the pprof-encoded profile
	* For each sample, use the location mapping and string table to
	  map the sample to its current value. We store this mapping, but first
	  look up any previous value we may have stored for that sample, so we can
	  compute the difference between the values.
	* If the difference is all 0s, the sample is unchanged, so we don't
	  emit it
	* Otherwise, we write a new sample record to the output. It will have
	  the same sequence of locations IDs and labels as the original, and will
	  have updated values.
	* If we keep a sample, record the location ID and the mapping and function
	  IDs associated with that location ID, so that we know to retain those
	  records.

Pass 3 (dropping un-needed records):
	* We copy every location, mapping, and function entry which we marked as
	  used in pass 2, from the original profile to the new profile.
	* For every other kind of record (besides samples, which we have already
	  written) copy from the original to the new profile unchanged.
    * Record string indices we need to write in Pass 4

Pass 4 (write out string table):
    * for strings referenced in the included messages
      (profile, function, mapping, value types, labels) write out to the
 	  delta buffer
    * for strings not referenced, write out a zero-length byte to save space
      while preserving index references in the included messages
*/

// DeltaComputer calculates the difference between pprof-encoded profiles
type DeltaComputer struct {
	// poisoned indicates that the previous delta computation ended
	// prematurely due to an error. This means the state of the
	// DeltaComputer is invalid, and the delta computer needs to be re-set
	poisoned bool

	// fields are the name and types of the values in a sample for which we should
	// compute the difference.
	fields []valueType // TODO(fg) refactor and remove

	// locationIndex associates location IDs (used by the pprof format to
	// cross-reference locations) to the actual instruction address of the
	// location
	locationIndex locationIndex
	// strings holds (hashed) copies of every string in the string table
	// of the current profile, used to hold the names of sample value types,
	// and the keys and values of labels.
	strings *stringTable

	curProfTimeNanos int64
	durationNanos    pproflite.DurationNanos

	decoder  pproflite.Decoder
	encoder  pproflite.Encoder
	deltaMap *DeltaMap

	// @TODO(fg) refactor and remove
	includeFunction SparseIntSet
	includeString   DenseIntSet
}

func newValueTypes(vts []pprofutils.ValueType) (ret []valueType) {
	for _, vt := range vts {
		ret = append(ret, valueType{Type: []byte(vt.Type), Unit: []byte(vt.Unit)})
	}
	return
}

type valueType struct {
	Type []byte
	Unit []byte
}

// NewDeltaComputer initializes a DeltaComputer which will calculate the
// difference between the values for profile samples whose fields have the given
// names (e.g. "alloc_space", "contention", ...)
func NewDeltaComputer(fields ...pprofutils.ValueType) *DeltaComputer {
	dc := &DeltaComputer{fields: newValueTypes(fields)}
	dc.initialize()
	return dc
}

func (dc *DeltaComputer) initialize() {
	dc.strings = newStringTable(murmur3.New128())
	dc.curProfTimeNanos = -1
	dc.deltaMap = NewDeltaMap(dc.strings, &dc.locationIndex, dc.fields)
}

func (dc *DeltaComputer) reset() {
	dc.strings.Reset()
	dc.locationIndex.Reset()
	dc.deltaMap.Reset()

	dc.includeFunction.Reset()
	dc.includeString.Reset()
}

// Delta calculates the difference between the pprof-encoded profile p and the
// profile passed in to the previous call to Delta. The encoded delta profile
// will be written to out.
//
// The first time Delta is called, the internal state of the DeltaComputer will
// be updated and the profile will be written unchanged.
func (dc *DeltaComputer) Delta(p []byte, out io.Writer) error {
	if err := dc.delta(p, out); err != nil {
		dc.poisoned = true
		return err
	}
	if dc.poisoned {
		// If we're recovering from a bad state, we'll use the first
		// profile to re-set the state. Technically the profile has
		// already been written to out, but we return an error to
		// indicate that the profile shouldn't be used.
		dc.poisoned = false
		return fmt.Errorf("delta profiler recovering from bad state, skipping this profile")
	}
	return nil
}

func (dc *DeltaComputer) delta(p []byte, out io.Writer) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("internal panic during delta profiling: %v", e)
		}
	}()

	if dc.poisoned {
		// If the last round failed, start fresh
		dc.initialize()
	}
	dc.reset()

	dc.encoder.Reset(out)
	dc.decoder.Reset(p)

	if err := dc.indexPass(); err != nil {
		return fmt.Errorf("indexPass: %w", err)
	} else if err := dc.mergeSamplePass(); err != nil {
		return fmt.Errorf("mergeSamplePass: %w", err)
	} else if err := dc.writeAndPruneRecordsPass(); err != nil {
		return fmt.Errorf("writeAndPruneRecordsPass: %w", err)
	} else if err := dc.functionPass(); err != nil {
		return fmt.Errorf("functionPass: %w", err)
	} else if err := dc.writeStringTablePass(); err != nil {
		return fmt.Errorf("writeStringTablePass: %w", err)
	}
	return nil
}

// This pass has the side effect of populating the indices:
//
//	valueTypeIndices
//	dc.locationIndex
//	dc.strings
//	dc.includeString (sizing)
func (dc *DeltaComputer) indexPass() error {
	strIdx := 0
	return dc.decoder.FieldEach(
		func(f pproflite.Field) error {
			switch t := f.(type) {
			case *pproflite.SampleType:
				if err := dc.deltaMap.AddSampleType(t); err != nil {
					return err
				}
			case *pproflite.Location:
				dc.locationIndex.Insert(t.ID, t.Address)
			case *pproflite.StringTable:
				dc.strings.Add(t.Value)
				// always include the zero-index empty string,
				// otherwise exclude by default unless used by a kept sample in mergeSamplesPass
				dc.includeString.Append(strIdx == 0)
				strIdx++
			default:
				return fmt.Errorf("indexPass: unexpected field: %T", f)
			}
			return nil
		},
		pproflite.SampleTypeDecoder,
		pproflite.LocationDecoder,
		pproflite.StringTableDecoder,
	)
}

// mergeSamplesPassFn returns a molecule callback to scan a Profile protobuf
// and write merged samples to the output buffer.
// Any samples where the values are all 0 will be skipped.
// This pass has the side effect of populating dc.include* fields so only the
// mapping, function, location, and strings (sample labels) referenced by the kept samples
// can be written out in a later pass.
// mergeSamplesPassFn returns a molecule callback to scan a Profile protobuf
// and write merged samples to the output buffer.
// Any samples where the values are all 0 will be skipped.
// This pass has the side effect of populating dc.include* fields so only the
// mapping, function, location, and strings (sample labels) referenced by the kept samples
// can be written out in a later pass.
func (dc *DeltaComputer) mergeSamplePass() error {
	return dc.decoder.FieldEach(
		func(f pproflite.Field) error {
			sample, ok := f.(*pproflite.Sample)
			if !ok {
				return fmt.Errorf("indexPass: unexpected field: %T", f)
			}

			if err := validStrings(sample, dc.strings); err != nil {
				return err
			}

			if hasNonZeroValues, err := dc.deltaMap.Delta(sample); err != nil {
				return err
			} else if !hasNonZeroValues {
				return nil
			}

			for _, locationID := range sample.LocationID {
				dc.locationIndex.MarkIncluded(locationID)
			}
			for _, l := range sample.Label {
				dc.includeString.Add(int(l.Key), int(l.Str), int(l.NumUnit))
			}
			return dc.encoder.Encode(sample)
		},
		pproflite.SampleDecoder,
	)
}

// writeAndPruneRecordsPassFn returns a molecule callback to scan a Profile protobuf
// and write out select mapping, location, and function records relevant to the
// selected samples (include* fields).
// Strings for the select records are collected for a later writing pass to
// populate the string table.
func (dc *DeltaComputer) writeAndPruneRecordsPass() error {
	firstPprof := dc.curProfTimeNanos < 0
	return dc.decoder.FieldEach(
		func(f pproflite.Field) error {
			switch t := f.(type) {
			case *pproflite.SampleType:
				dc.includeString.Add(int(t.Unit), int(t.Type))
			case *pproflite.Mapping:
				dc.includeString.Add(int(t.Filename), int(t.BuildID))
			case *pproflite.LocationFast:
				if !dc.locationIndex.Included(t.ID) {
					return nil
				}
				for _, funcID := range t.FunctionID {
					dc.includeFunction.Add(int(funcID))
				}
			case *pproflite.DropFrames:
				dc.includeString.Add(int(t.Value))
			case *pproflite.KeepFrames:
				dc.includeString.Add(int(t.Value))
			case *pproflite.TimeNanos:
				curProfTimeNanos := int64(t.Value)
				if !firstPprof {
					prevProfTimeNanos := dc.curProfTimeNanos
					if err := dc.encoder.Encode(t); err != nil {
						return err
					}
					dc.durationNanos.Value = curProfTimeNanos - prevProfTimeNanos
					return dc.encoder.Encode(&dc.durationNanos)
				}
				dc.curProfTimeNanos = curProfTimeNanos
			case *pproflite.DurationNanos:
				if !firstPprof {
					return nil
				}
			case *pproflite.PeriodType:
				dc.includeString.Add(int(t.Unit), int(t.Type))
			case *pproflite.Period:
			case *pproflite.Comment:
				dc.includeString.Add(int(t.Value))
			case *pproflite.DefaultSampleType:
				dc.includeString.Add(int(t.Value))
			default:
				return fmt.Errorf("unexpected field: %T", f)
			}
			return dc.encoder.Encode(f)
		},
		pproflite.SampleTypeDecoder,
		pproflite.MappingDecoder,
		pproflite.LocationFastDecoder,
		pproflite.DropFramesDecoder,
		pproflite.KeepFramesDecoder,
		pproflite.TimeNanosDecoder,
		pproflite.DurationNanosDecoder,
		pproflite.PeriodTypeDecoder,
		pproflite.PeriodDecoder,
		pproflite.CommentDecoder,
		pproflite.DefaultSampleTypeDecoder,
	)
}

func (dc *DeltaComputer) functionPass() error {
	return dc.decoder.FieldEach(
		func(f pproflite.Field) error {
			switch t := f.(type) {
			case *pproflite.Function:
				if !dc.includeFunction.Contains(int(t.ID)) {
					return nil
				}
				dc.includeString.Add(int(t.Name), int(t.SystemName), int(t.FileName))
			default:
				return fmt.Errorf("unexpected field: %T", f)
			}
			return dc.encoder.Encode(f)
		},
		pproflite.FunctionDecoder,
	)
}

// writeStringTablePassFn returns a molecule callback to scan a Profile protobuf
// and write out string table messages to buf.
// Strings marked for emission in `dc.includeString` are written to buf.
// Strings not marked for emission are written as zero-length byte arrays
// to preserve index offsets.
func (dc *DeltaComputer) writeStringTablePass() error {
	counter := 0
	return dc.decoder.FieldEach(
		func(f pproflite.Field) error {
			str, ok := f.(*pproflite.StringTable)
			if !ok {
				return fmt.Errorf("stringTablePass: unexpected field: %T", f)
			}
			if !dc.includeString.Contains(counter) {
				str.Value = nil
			}
			counter++
			return dc.encoder.Encode(str)
		},
		pproflite.StringTableDecoder,
	)
}

// TODO(fg) we should probably validate all strings?
func validStrings(s *pproflite.Sample, st *stringTable) error {
	for _, l := range s.Label {
		if !st.Contains(uint64(l.Key)) {
			return fmt.Errorf("invalid string index %d", l.Key)
		}
		if !st.Contains(uint64(l.Str)) {
			return fmt.Errorf("invalid string index %d", l.Str)
		}
		if !st.Contains(uint64(l.NumUnit)) {
			return fmt.Errorf("invalid string index %d", l.NumUnit)
		}
	}
	return nil
}
