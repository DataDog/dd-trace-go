// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package fastdelta

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"sort"

	"github.com/richardartoul/molecule"
	"github.com/richardartoul/molecule/src/codec"
	"github.com/richardartoul/molecule/src/protowire"
	"github.com/spaolacci/murmur3"

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
	fields []pprofutils.ValueType

	// locationIndex associates location IDs (used by the pprof format to
	// cross-reference locations) to the actual instruction address of the
	// location
	locationIndex locationIndex
	// strings holds (hashed) copies of every string in the string table
	// of the current profile, used to hold the names of sample value types,
	// and the keys and values of labels.
	strings *stringTable
	// sampleMap holds the value of a sample, as represented by a consistent
	// hash of its call stack and labels, to the value of the sample for the
	// last time that sample was observed.
	sampleMap map[Hash]sampleValue

	curProfTimeNanos int64

	// valueTypeIndices are string table indices of the sample value type
	// names (e.g.  "alloc_space", "cycles"...) and their types ("count", "bytes")
	// included in DeltaComputer as the indices are written in indexPass
	// and read in mergeSamplesPass
	valueTypeIndices [][2]int

	// saves some heap allocations
	scratch          [128]byte
	scratchIDs       []uint64
	scratchAddresses []uint64
	hashes           byHash

	// include* is for pruning the delta output, populated on merge pass
	includeMapping  map[uint64]struct{}
	includeFunction map[uint64]struct{}
	includeLocation map[uint64]struct{}
	includeString   []bool
}

// NewDeltaComputer initializes a DeltaComputer which will calculate the
// difference between the values for profile samples whose fields have the given
// names (e.g. "alloc_space", "contention", ...)
func NewDeltaComputer(fields ...pprofutils.ValueType) *DeltaComputer {
	dc := &DeltaComputer{
		fields: append([]pprofutils.ValueType{}, fields...),
	}
	dc.initialize()
	return dc
}

func (dc *DeltaComputer) initialize() {
	dc.sampleMap = make(map[Hash]sampleValue)
	dc.scratchIDs = make([]uint64, 0, 512)
	dc.includeMapping = make(map[uint64]struct{})
	dc.includeFunction = make(map[uint64]struct{})
	dc.includeLocation = make(map[uint64]struct{})
	dc.includeString = make([]bool, 0, 1024)
	dc.strings = newStringTable(murmur3.New128())
	dc.curProfTimeNanos = -1
}

func (dc *DeltaComputer) reset() {
	dc.strings.h = dc.strings.h[:0]
	dc.locationIndex.Reset()

	// reset bookkeeping for message pruning
	// go compiler should convert these to single runtime.mapclear calls
	for k := range dc.includeMapping {
		delete(dc.includeMapping, k)
	}
	for k := range dc.includeFunction {
		delete(dc.includeFunction, k)
	}
	for k := range dc.includeLocation {
		delete(dc.includeLocation, k)
	}
	dc.includeString = dc.includeString[:0]
	dc.valueTypeIndices = dc.valueTypeIndices[:0]
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
	hasher := murmur3.New128()
	dc.reset()

	if dc.poisoned {
		// If the last round failed, start fresh
		dc.initialize()
	}

	err = molecule.MessageEach(codec.NewBuffer(p), dc.indexPassFn(hasher))
	if err != nil {
		return fmt.Errorf("error in indexing pass: %w", err)
	}

	if len(dc.valueTypeIndices) > maxSampleValues {
		return fmt.Errorf("profile has %d values per sample, exceeding the maximum %d", len(dc.valueTypeIndices), maxSampleValues)
	}

	// TODO: first pass optimization, if this is the first profile DeltaComputer consumes,
	// would be to compute the previous values to populate dc.sampleMap, but just return
	// the original profile bytes rather than effectively copying them
	err = molecule.MessageEach(codec.NewBuffer(p), dc.mergeSamplesPassFn(out, hasher))
	if err != nil {
		return fmt.Errorf("error in merge samples pass: %w", err)
	}

	err = molecule.MessageEach(codec.NewBuffer(p), dc.writeAndPruneRecordsPassFn(out))
	if err != nil {
		return fmt.Errorf("error in pruning pass: %w", err)
	}

	err = molecule.MessageEach(codec.NewBuffer(p), dc.writeStringTablePassFn(out))
	if err != nil {
		return fmt.Errorf("error in string table writing pass: %w", err)
	}

	return nil
}

// indexPassFn returns a molecule callback to scan a Profile protobuf
// This pass has the side effect of populating the indices:
//
//	valueTypeIndices
//	dc.locationIndex
//	dc.strings
//	dc.includeString (sizing)
func (dc *DeltaComputer) indexPassFn(hasher murmur3.Hash128) molecule.MessageEachFn {
	return func(field int32, value molecule.Value) (bool, error) {
		return dc.indexPass(field, value, hasher)
	}
}

func (dc *DeltaComputer) indexPass(field int32, value molecule.Value, hasher murmur3.Hash128) (bool, error) {
	switch ProfileRecordNumber(field) {
	case recProfileSampleType:
		vType, vUnit, err := dc.readValueType(value.Bytes)
		if err != nil {
			return false, fmt.Errorf("reading sample_type record: %w", err)
		}
		dc.valueTypeIndices = append(dc.valueTypeIndices, [2]int{vType, vUnit})
	case recProfileLocation:
		// readLocation writes out function IDs for the location to this scratch buffer
		dc.scratchIDs = dc.scratchIDs[:0]
		address, id, mappingID, err := dc.readLocation(value.Bytes)
		if err != nil {
			return false, fmt.Errorf("reading location record: %w", err)
		}
		dc.locationIndex.Insert(id, address, mappingID, dc.scratchIDs)
	case recProfileStringTable:
		dc.strings.add(value.Bytes)

		// always include the zero-index empty string,
		// otherwise exclude by default unless used by a kept sample in mergeSamplesPass
		dc.includeString = append(dc.includeString, len(dc.includeString) == 0)
	}
	return true, nil
}

// mergeSamplesPassFn returns a molecule callback to scan a Profile protobuf
// and write merged samples to the output buffer.
// Any samples where the values are all 0 will be skipped.
// This pass has the side effect of populating dc.include* fields so only the
// mapping, function, location, and strings (sample labels) referenced by the kept samples
// can be written out in a later pass.
func (dc *DeltaComputer) mergeSamplesPassFn(out io.Writer, hasher murmur3.Hash128) molecule.MessageEachFn {
	var sampleHash Hash

	computeDeltaForValue := make([]bool, len(dc.valueTypeIndices))
	for _, field := range dc.fields {
		for i, vtIdxs := range dc.valueTypeIndices {
			typeMatch := dc.strings.Equals(vtIdxs[0], []byte(field.Type))
			unitMatch := dc.strings.Equals(vtIdxs[1], []byte(field.Unit))
			if typeMatch && unitMatch {
				computeDeltaForValue[i] = true
				break
			}
		}
	}

	return func(field int32, value molecule.Value) (bool, error) {
		return dc.mergeSamplesPass(field, value, out, hasher, &sampleHash, computeDeltaForValue)
	}
}

func (dc *DeltaComputer) mergeSamplesPass(
	field int32,
	value molecule.Value,
	out io.Writer,
	hasher murmur3.Hash128,
	sampleHash *Hash,
	computeDeltaForValue []bool) (bool, error) {
	if ProfileRecordNumber(field) != recProfileSample {
		return true, nil
	}

	// readSample writes out the locations for this sample into this scratch buffer
	// to save on allocations
	dc.scratchIDs = dc.scratchIDs[:0]
	val, err := dc.readSample(value.Bytes, hasher, sampleHash)
	if err != nil {
		return false, fmt.Errorf("reading sample record: %w", err)
	}

	old, ok := dc.sampleMap[*sampleHash]
	dc.sampleMap[*sampleHash] = val // save for next time
	if !ok {
		// If this is a new sample we don't take the
		// difference, just pass it through.
		// but we should record the value for next time
		if err := dc.writeProtoBytes(out, field, value.Bytes); err != nil {
			return false, err
		}
		dc.keepLocations(dc.scratchIDs)
		return true, nil
	}

	all0 := true
	for i := 0; i < len(computeDeltaForValue); i++ {
		if computeDeltaForValue[i] {
			val[i] = val[i] - old[i]
		}
		if val[i] != 0 {
			all0 = false
		}
	}
	if all0 {
		// If the sample has all 0 values, we drop it
		// this matches the behavior of Google's pprof library
		// when merging profiles
		return true, nil
	}

	dc.keepLocations(dc.scratchIDs)

	// we want to write a modified version of the original record, where the only difference is
	// the values
	sample := make([]byte, 0, len(value.Bytes))
	err = molecule.MessageEach(codec.NewBuffer(value.Bytes), func(field int32, value molecule.Value) (bool, error) {
		switch SampleRecordNumber(field) {
		case recSampleLocationID:
			// retain the old stack
			switch value.WireType {
			case codec.WireBytes:
				sample = appendProtoBytes(sample, field, value.Bytes)
			case codec.WireVarint:
				sample = appendProtoUvarint(sample, field, value.Number)
			}
		case recSampleLabel:
			// retain the old labels
			sample = appendProtoBytes(sample, field, value.Bytes)

			// mark strings to keep in the labels structure
			err := dc.includeStringIndexFields(value.Bytes,
				int32(recLabelKey), int32(recLabelStr), int32(recLabelNumUnit))
			return err == nil, err
		}
		return true, nil
	})
	if err != nil {
		return false, err
	}
	newValue := make([]byte, 0, 8*4)
	for i := range dc.valueTypeIndices {
		newValue = protowire.AppendVarint(newValue, uint64(val[i]))
	}
	sample = appendProtoBytes(sample, int32(recSampleValue), newValue)

	if err := dc.writeProtoBytes(out, field, sample); err != nil {
		return false, err
	}

	return true, nil
}

// writeAndPruneRecordsPassFn returns a molecule callback to scan a Profile protobuf
// and write out select mapping, location, and function records relevant to the
// selected samples (include* fields).
// Strings for the select records are collected for a later writing pass to
// populate the string table.
func (dc *DeltaComputer) writeAndPruneRecordsPassFn(out io.Writer) molecule.MessageEachFn {
	firstPprof := dc.curProfTimeNanos < 0
	return func(field int32, value molecule.Value) (bool, error) {
		return dc.writeAndPruneRecordsPass(field, value, out, firstPprof)
	}
}

func (dc *DeltaComputer) writeAndPruneRecordsPass(field int32, value molecule.Value, out io.Writer, firstPprof bool) (bool, error) {
	switch ProfileRecordNumber(field) {
	case recProfileSample:
		// already written these out, skip
		return true, nil
	case recProfileMapping:
		id, err := dc.readUint64Field(value.Bytes, int32(recMappingID))
		if err != nil {
			return false, fmt.Errorf("reading mapping record: %w", err)
		}
		if _, ok := dc.includeMapping[id]; !ok {
			return true, nil
		}
		// mark strings to keep from the mapping message
		err = dc.includeStringIndexFields(value.Bytes,
			int32(recMappingFilename), int32(recMappingBuildID))
		if err != nil {
			return false, fmt.Errorf("reading mapping record: %w", err)
		}
	case recProfileLocation:
		id, err := dc.readUint64Field(value.Bytes, int32(recLocationID))
		if err != nil {
			return false, fmt.Errorf("reading location record: %w", err)
		}
		if _, ok := dc.includeLocation[id]; !ok {
			return true, nil
		}
	case recProfileFunction:
		id, err := dc.readUint64Field(value.Bytes, int32(recFunctionID))
		if err != nil {
			return false, fmt.Errorf("reading function record: %w", err)
		}
		if _, ok := dc.includeFunction[id]; !ok {
			return true, nil
		}
		// mark strings to keep from the function message
		err = dc.includeStringIndexFields(value.Bytes,
			int32(recFunctionName), int32(recFunctionSystemName), int32(recFunctionFilename))
		if err != nil {
			return false, fmt.Errorf("reading function record: %w", err)
		}
	case recProfileComment:
		// comment - repeated int64 indices into string table
		// repeated fields are packed by default, but we can see both packed or single values.
		// we need to include comment indices in dc.includeString for writeStringTablePassFn
		switch value.WireType {
		case codec.WireBytes:
			err := iterPackedVarints(value.Bytes, func(index uint64) {
				dc.markStringIncluded(uint64(index))
			})
			if err != nil {
				return false, err
			}
		case codec.WireVarint:
			dc.markStringIncluded(value.Number)
		}
	case recProfileDropFrames, recProfileKeepFrames, recProfileDefaultSampleType:
		dc.markStringIncluded(value.Number)
	case recProfileSampleType, recProfilePeriodType:
		err := dc.includeStringIndexFields(value.Bytes,
			int32(recValueTypeType), int32(recValueTypeUnit))
		if err != nil {
			return false, fmt.Errorf("reading ValueType record: %w", err)
		}
	case recProfileTimeNanos:
		curProfTimeNanos := int64(value.Number)
		if !firstPprof {
			prevProfTimeNanos := dc.curProfTimeNanos
			if err := dc.writeValue(out, field, value); err != nil {
				return false, err
			}
			field = int32(recProfileDurationNanos)
			value.Number = uint64(curProfTimeNanos - prevProfTimeNanos)
		}
		dc.curProfTimeNanos = curProfTimeNanos
	case recProfileDurationNanos:
		if !firstPprof {
			return true, nil // skip, it's written together with recProfileTimeNanos
		}
		// otherwise, just copy through
	case recProfileStringTable:
		return true, nil // will write these on the string writing pass
	}

	// If it's not a sample or string, and it's not pruned just write it out
	if err := dc.writeValue(out, field, value); err != nil {
		return false, err
	}
	return true, nil
}

// writeStringTablePassFn returns a molecule callback to scan a Profile protobuf
// and write out string table messages to buf.
// Strings marked for emission in `dc.includeString` are written to buf.
// Strings not marked for emission are written as zero-length byte arrays
// to preserve index offsets.
func (dc *DeltaComputer) writeStringTablePassFn(out io.Writer) molecule.MessageEachFn {
	counter := 0
	return func(field int32, value molecule.Value) (bool, error) {
		return dc.writeStringTablePass(field, value, out, &counter)
	}
}

func (dc *DeltaComputer) writeStringTablePass(field int32, value molecule.Value, out io.Writer, counter *int) (bool, error) {
	var stringVal []byte
	switch ProfileRecordNumber(field) {
	case recProfileStringTable:
		if dc.isStringIncluded(*counter) {
			stringVal = value.Bytes
		}
		*counter++
	default:
		// everything else has already been written
		return true, nil
	}
	if err := dc.writeProtoBytes(out, field, stringVal); err != nil {
		return false, err
	}
	return true, nil
}

func (dc *DeltaComputer) writeValue(w io.Writer, field int32, value molecule.Value) error {
	buf := dc.scratch[:0]
	switch value.WireType {
	case codec.WireVarint:
		buf = appendProtoUvarint(buf, field, value.Number)
	case codec.WireBytes:
		buf = appendProtoBytes(buf, field, value.Bytes)
	}
	_, err := w.Write(buf)
	return err
}

func appendProtoBytes(b []byte, field int32, value []byte) []byte {
	b = protowire.AppendVarint(b, uint64((field<<3)|int32(codec.WireBytes)))
	b = protowire.AppendVarint(b, uint64(len(value)))
	return append(b, value...)
}

func (dc *DeltaComputer) writeProtoBytes(w io.Writer, field int32, value []byte) error {
	b := appendProtoBytes(dc.scratch[:0], field, value)
	_, err := w.Write(b)
	return err
}

func appendProtoUvarint(b []byte, field int32, value uint64) []byte {
	b = protowire.AppendVarint(b, uint64((field<<3)|int32(codec.WireVarint)))
	return protowire.AppendVarint(b, value)
}

func (dc *DeltaComputer) readValueType(v []byte) (vType, vUnit int, err error) {
	err = molecule.MessageEach(codec.NewBuffer(v), func(field int32, value molecule.Value) (bool, error) {
		switch ValueTypeRecordNumber(field) {
		case recValueTypeType:
			vType = int(value.Number)
		case recValueTypeUnit:
			vUnit = int(value.Number)
		}
		return true, nil
	})
	if err != nil {
		return 0, 0, err
	}
	return vType, vUnit, nil
}

func (dc *DeltaComputer) readLocation(v []byte) (address, id, mappingID uint64, err error) {
	err = molecule.MessageEach(codec.NewBuffer(v), func(field int32, value molecule.Value) (bool, error) {
		switch LocationRecordNumber(field) {
		case recLocationID:
			id = value.Number
		case recLocationMappingID:
			mappingID = value.Number
		case recLocationAddress:
			address = value.Number
		case recLocationLine:
			// collect function_id from repeated Line
			err := molecule.MessageEach(codec.NewBuffer(value.Bytes), func(field int32, value molecule.Value) (bool, error) {
				switch LineRecordNumber(field) {
				case recLineFunctionID:
					dc.scratchIDs = append(dc.scratchIDs, value.Number)
					return false, nil
				}
				return true, nil
			})
			if err != nil {
				return false, err
			}
		}
		return true, nil
	})
	return
}

func (dc *DeltaComputer) readUint64Field(v []byte, recordNumber int32) (val uint64, err error) {
	err = molecule.MessageEach(codec.NewBuffer(v), func(field int32, value molecule.Value) (bool, error) {
		switch field {
		case recordNumber:
			val = value.Number
			return false, nil
		}
		return true, nil
	})
	return
}

func (dc *DeltaComputer) readSample(v []byte, h hash.Hash, hash *Hash) (value sampleValue, err error) {
	values := value[:0]
	dc.hashes = dc.hashes[:0]
	dc.scratchAddresses = dc.scratchAddresses[:0]
	err = molecule.MessageEach(codec.NewBuffer(v), func(field int32, value molecule.Value) (bool, error) {
		switch SampleRecordNumber(field) {
		case recSampleLocationID:
			// location ID - repeated uint64
			// repeated fields are packed by default
			// see https://developers.google.com/protocol-buffers/docs/encoding#packed,
			// but we can see both packed (bytes of concatenated PCs)
			// or one/two single values. The Go runtime pprof encoding implementation
			// will only pack when there are more than 2 locations for a sample
			// (ref: https://cs.opensource.google/go/go/+/master:src/runtime/pprof/proto.go;l=527;drc=403f91c24430213b6a8efb3d143b6eae08b02ec2;bpv=1;bpt=1)
			switch value.WireType {
			case codec.WireBytes:
				err := iterPackedVarints(value.Bytes, func(id uint64) {
					addr, ok := dc.locationIndex.Get(id)
					if !ok {
						return
					}
					dc.scratchAddresses = append(dc.scratchAddresses, addr)
					dc.scratchIDs = append(dc.scratchIDs, id)
				})
				if err != nil {
					return false, err
				}
			case codec.WireVarint:
				addr, ok := dc.locationIndex.Get(value.Number)
				if !ok {
					return false, fmt.Errorf("invalid location index")
				}
				dc.scratchAddresses = append(dc.scratchAddresses, addr)
				dc.scratchIDs = append(dc.scratchIDs, value.Number)
			}
		case recSampleValue:
			// repeated int64
			switch value.WireType {
			case codec.WireBytes:
				err := iterPackedVarints(value.Bytes, func(n uint64) {
					values = append(values, int64(n))
				})
				if err != nil {
					return false, err
				}
			case codec.WireVarint:
				values = append(values, int64(value.Number))
			}
		case recSampleLabel:
			var (
				key  uint64
				num  uint64
				unit uint64
			)
			const sentinel = 0xffffffffffffffff
			str := uint64(sentinel) // sentinel for "value is numeric, not a string"
			err := molecule.MessageEach(codec.NewBuffer(value.Bytes), func(field int32, value molecule.Value) (bool, error) {
				switch LabelRecordNumber(field) {
				case recLabelKey:
					key = value.Number
				case recLabelStr:
					str = value.Number
				case recLabelNum:
					num = value.Number
				case recLabelNumUnit:
					unit = value.Number
				}
				return true, nil
			})
			if err != nil {
				return false, err
			}
			if !dc.strings.contains(key) {
				return false, fmt.Errorf("invalid string index %d", key)
			}
			if str != sentinel && !dc.strings.contains(str) {
				return false, fmt.Errorf("invalid string index %d", str)
			}
			if !dc.strings.contains(unit) {
				return false, fmt.Errorf("invalid string index %d", unit)
			}
			h.Reset()
			h.Write(dc.strings.h[uint(key)][:])
			h.Write(dc.strings.h[uint(unit)][:])
			binary.BigEndian.PutUint64(dc.scratch[:], num)
			h.Write(dc.scratch[:8])
			if str < uint64(len(dc.strings.h)) {
				h.Write(dc.strings.h[uint(str)][:])
			}
			h.Sum(hash[:0])
			dc.hashes = append(dc.hashes, *hash)
		}
		return true, nil
	})

	h.Reset()
	for _, addr := range dc.scratchAddresses {
		binary.LittleEndian.PutUint64(dc.scratch[:], addr)
		h.Write(dc.scratch[:8])
	}

	// Memory profiles current have exactly one label ("bytes"), so there is no
	// need to sort. This saves ~0.5% of CPU time in our benchmarks.
	if len(dc.hashes) > 1 {
		sort.Sort(&dc.hashes) // passing &dc.hashes vs dc.hashes avoids an alloc here
	}

	for _, sub := range dc.hashes {
		copy(hash[:], sub[:]) // avoid sub escape to heap
		h.Write(hash[:])
	}
	h.Sum(hash[:0])
	return
}

// includeStringIndexFields marks strings for inclusion from a message's fields.
// `fieldIndexes` specifies which field offsets in the message have indexes
// into the Profile string table.
func (dc *DeltaComputer) includeStringIndexFields(msgBytes []byte, fieldIndexes ...int32) error {
	return molecule.MessageEach(codec.NewBuffer(msgBytes), func(field int32, value molecule.Value) (bool, error) {
		for _, fieldIdx := range fieldIndexes {
			if field == fieldIdx {
				dc.markStringIncluded(value.Number)
			}
		}
		return true, nil
	})
}

func (dc *DeltaComputer) keepLocations(locationIDs []uint64) {
	for _, locationID := range locationIDs {
		mappingID, functionIDs, ok := dc.locationIndex.GetMeta(locationID)
		if !ok {
			continue
		}
		dc.includeMapping[mappingID] = struct{}{}
		dc.includeLocation[locationID] = struct{}{}
		for _, functionID := range functionIDs {
			dc.includeFunction[functionID] = struct{}{}
		}
	}
}

// isStringIncluded returns whether the string at table index i should be
// included in the profile
func (dc *DeltaComputer) isStringIncluded(i int) bool {
	if i < 0 || i >= len(dc.includeString) {
		return false
	}
	return dc.includeString[i]
}

// markStringIncluded records that the string at table index i should be
// included in the profile
func (dc *DeltaComputer) markStringIncluded(i uint64) {
	if i < uint64(len(dc.includeString)) {
		dc.includeString[i] = true
	}
	// TODO: panic otherwise?
}

// iterPackedVarints calls f for every varint packed in b.
// Returns an error if b is malformed.
func iterPackedVarints(b []byte, f func(n uint64)) error {
	for len(b) > 0 {
		v, n := binary.Uvarint(b)
		if n <= 0 {
			return fmt.Errorf("invalid varint")
		}
		f(v)
		b = b[n:]
	}
	return nil
}

// Hash is a 128-bit hash representing sample identity
type Hash [16]byte

type byHash []Hash

func (h byHash) Len() int           { return len(h) }
func (h byHash) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h byHash) Less(i, j int) bool { return bytes.Compare(h[i][:], h[j][:]) == -1 }

// As of Go 1.19, the Go heap profile has 4 values per sample. This is the most
// for any of the Go runtime profiles. In order to make the map of samples to
// their values more GC-friendly, we prefer to have the values for that mapping
// be fixed-size arrays rather than slices. However, this means we can't process
// profiles with more than this many values per sample.
const maxSampleValues = 4

type sampleValue [maxSampleValues]int64

type stringTable struct {
	// Passing a byte slice to hash.Hash causes it to escape to the heap, so
	// we keep around a single Hash to reuse to avoid a new allocation every
	// time we add an element to the string table
	reuse  Hash
	h      []Hash
	hasher hash.Hash
}

func newStringTable(h hash.Hash) *stringTable {
	return &stringTable{hasher: h}
}

// contains returns whether i is a valid index for the string table
func (s *stringTable) contains(i uint64) bool {
	return i < uint64(len(s.h))
}

func (s *stringTable) add(b []byte) {
	s.hasher.Reset()
	s.hasher.Write(b)
	s.hasher.Sum(s.reuse[:0])
	s.h = append(s.h, s.reuse)
}

// Equals returns whether the value at index i equals the byte string b
func (s *stringTable) Equals(i int, b []byte) bool {
	s.hasher.Reset()
	s.hasher.Write(b)
	s.hasher.Sum(s.reuse[:0])
	return s.reuse == s.h[i]
}
