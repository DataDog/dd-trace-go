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
	// fields are the names of the values in a sample for which we should
	// compute the difference.
	fields []string

	// locationIndex associates location IDs (used by the pprof format to
	// cross-reference locations) to the actual instruction address of the
	// location
	locationIndex locationIndex
	// stringTable holds (hashed) copies of every string in the string table
	// of the current profile, used to hold the names of sample value types,
	// and the keys and values of labels.
	stringTable StringTable
	// sampleMap holds the value of a sample, as represented by a consistent
	// hash of its call stack and labels, to the value of the sample for the
	// last time that sample was observed.
	sampleMap map[Hash]Value

	curProfTimeNanos int64

	// saves some heap allocations
	scratch    [128]byte
	scratchIDs []uint64
	hashes     []Hash

	// include* is for pruning the delta output, populated on merge pass
	includeMapping  map[uint64]struct{}
	includeFunction map[uint64]struct{}
	includeLocation map[uint64]struct{}
	includeString   []bool
}

// NewDeltaComputer initializes a DeltaComputer which will calculate the
// difference between the values for profile samples whose fields have the given
// names (e.g. "alloc_space", "contention", ...)
func NewDeltaComputer(fields ...string) *DeltaComputer {
	return &DeltaComputer{
		fields:           append([]string{}, fields...),
		sampleMap:        make(map[Hash]Value),
		scratchIDs:       make([]uint64, 0, 512),
		includeMapping:   make(map[uint64]struct{}),
		includeFunction:  make(map[uint64]struct{}),
		includeLocation:  make(map[uint64]struct{}),
		includeString:    make([]bool, 0, 1024),
		curProfTimeNanos: -1,
	}
}

func (dc *DeltaComputer) reset() {
	dc.stringTable.h = dc.stringTable.h[:0]
	dc.locationIndex.reset()

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
}

// Delta calculates the difference between the pprof-encoded profile p and the
// profile passed in to the previous call to Delta. The encoded delta profile
// will be written to out.
//
// The first time Delta is called, the internal state of the DeltaComputer will
// be updated and the profile will be written unchanged.
func (dc *DeltaComputer) Delta(p []byte, out io.Writer) error {
	hasher := murmur3.New128()
	dc.reset()

	// valueTypeIndices are string table indices of the sample value type
	// names (e.g.  "alloc_space", "cycles"...)
	var valueTypeIndices []int
	err := molecule.MessageEach(codec.NewBuffer(p),
		dc.indexPass(&valueTypeIndices, hasher))
	if err != nil {
		return fmt.Errorf("error in indexing pass: %w", err)
	}

	// TODO: first pass optimization, if this is the first profile DeltaComputer consumes,
	// would be to compute the previous values to populate dc.sampleMap, but just return
	// the original profile bytes rather than effectively copying them
	err = molecule.MessageEach(codec.NewBuffer(p),
		dc.mergeSamplesPass(out, valueTypeIndices, hasher))
	if err != nil {
		return fmt.Errorf("error in merge samples pass: %w", err)
	}

	err = molecule.MessageEach(codec.NewBuffer(p),
		dc.writeAndPruneRecordsPass(out))
	if err != nil {
		return fmt.Errorf("error in pruning pass: %w", err)
	}

	err = molecule.MessageEach(codec.NewBuffer(p),
		dc.writeStringTablePass(out))
	if err != nil {
		return fmt.Errorf("error in string table writing pass: %w", err)
	}

	return nil
}

// indexPass returns a molecule callback to scan a Profile protobuf
// This pass has the side effect of populating the indices:
//
//	valueTypeIndices
//	dc.locationIndex
//	dc.stringTable
//	dc.includeString (sizing)
func (dc *DeltaComputer) indexPass(valueTypeIndices *[]int, hasher murmur3.Hash128) molecule.MessageEachFn {
	return func(field int32, value molecule.Value) (bool, error) {
		switch ProfileRecordNumber(field) {
		case recProfileSampleType:
			err := molecule.MessageEach(codec.NewBuffer(value.Bytes), func(field int32, value molecule.Value) (bool, error) {
				switch ValueTypeRecordNumber(field) {
				case recValueTypeType:
					*valueTypeIndices = append(*valueTypeIndices, int(value.Number))
				}
				return true, nil
			})
			if err != nil {
				return false, fmt.Errorf("reading sample_type record: %w", err)
			}
		case recProfileLocation:
			// readLocation writes out function IDs for the location to this scratch buffer
			dc.scratchIDs = dc.scratchIDs[:0]
			address, id, mappingID, err := dc.readLocation(value.Bytes)
			if err != nil {
				return false, fmt.Errorf("reading location record: %w", err)
			}
			dc.locationIndex.insert(id, address, mappingID, dc.scratchIDs)
		case recProfileStringTable:
			dc.stringTable.Add(value.Bytes, hasher)

			// always include the zero-index empty string,
			// otherwise exclude by default unless used by a kept sample in mergeSamplesPass
			dc.includeString = append(dc.includeString, len(dc.includeString) == 0)
		}
		return true, nil
	}
}

// mergeSamplesPass returns a molecule callback to scan a Profile protobuf
// and write merged samples to the output buffer.
// Any samples where the values are all 0 will be skipped.
// This pass has the side effect of populating dc.include* fields so only the
// mapping, function, location, and strings (sample labels) referenced by the kept samples
// can be written out in a later pass.
func (dc *DeltaComputer) mergeSamplesPass(out io.Writer, valueTypeIndices []int, hasher murmur3.Hash128) molecule.MessageEachFn {
	var sampleHash Hash

	computeDeltaForValue := make([]bool, len(valueTypeIndices))
	for _, field := range dc.fields {
		for i, j := range valueTypeIndices {
			if dc.stringTable.Equals(j, []byte(field), hasher) {
				computeDeltaForValue[i] = true
				break
			}
		}
	}

	return func(field int32, value molecule.Value) (bool, error) {
		switch ProfileRecordNumber(field) {
		case recProfileSample:
			// readSample writes out the locations for this sample into this scratch buffer
			// to save on allocations
			dc.scratchIDs = dc.scratchIDs[:0]
			val, err := dc.readSample(value.Bytes, hasher, &sampleHash)
			if err != nil {
				return false, fmt.Errorf("reading sample record: %w", err)
			}

			old, ok := dc.sampleMap[sampleHash]
			dc.sampleMap[sampleHash] = val // save for next time
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
			for i := range valueTypeIndices {
				newValue = protowire.AppendVarint(newValue, uint64(val[i]))
			}
			sample = appendProtoBytes(sample, int32(recSampleValue), newValue)

			if err := dc.writeProtoBytes(out, field, sample); err != nil {
				return false, err
			}
		}
		return true, nil
	}
}

// writeAndPruneRecordsPass returns a molecule callback to scan a Profile protobuf
// and write out select mapping, location, and function records relevant to the
// selected samples (include* fields).
// Strings for the select records are collected for a later writing pass to
// populate the string table.
func (dc *DeltaComputer) writeAndPruneRecordsPass(out io.Writer) molecule.MessageEachFn {
	firstPprof := dc.curProfTimeNanos < 0
	return func(field int32, value molecule.Value) (bool, error) {
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
			// we need to include comment indices in dc.includeString for writeStringTablePass
			switch value.WireType {
			case codec.WireBytes:
				bs := value.Bytes
				for len(bs) > 0 {
					index, n := binary.Varint(bs)
					bs = bs[n:]
					dc.includeString[index] = true
				}
			case codec.WireVarint:
				dc.includeString[value.Number] = true
			}
		case recProfileDropFrames, recProfileKeepFrames, recProfileDefaultSampleType:
			dc.includeString[value.Number] = true
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
}

// writeStringTablePass returns a molecule callback to scan a Profile protobuf
// and write out string table messages to buf.
// Strings marked for emission in `dc.includeString` are written to buf.
// Strings not marked for emission are written as zero-length byte arrays
// to preserve index offsets.
func (dc *DeltaComputer) writeStringTablePass(out io.Writer) molecule.MessageEachFn {
	counter := 0
	return func(field int32, value molecule.Value) (bool, error) {
		var stringVal []byte
		switch ProfileRecordNumber(field) {
		case recProfileStringTable:
			if dc.includeString[counter] {
				stringVal = value.Bytes
			}
			counter++
		default:
			// everything else has already been written
			return true, nil
		}
		if err := dc.writeProtoBytes(out, field, stringVal); err != nil {
			return false, err
		}
		return true, nil
	}
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

func (dc *DeltaComputer) readSample(v []byte, h hash.Hash, hash *Hash) (value Value, err error) {
	values := value[:0]
	dc.hashes = dc.hashes[:0]
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
			h.Reset()
			switch value.WireType {
			case codec.WireBytes:
				bs := value.Bytes
				for len(bs) > 0 {
					x, n := binary.Uvarint(bs)
					binary.BigEndian.PutUint64(dc.scratch[:], dc.locationIndex.get(x))
					h.Write(dc.scratch[:8])
					bs = bs[n:]
					dc.scratchIDs = append(dc.scratchIDs, x)
				}
			case codec.WireVarint:
				binary.BigEndian.PutUint64(dc.scratch[:], dc.locationIndex.get(value.Number))
				h.Write(dc.scratch[:8])
				dc.scratchIDs = append(dc.scratchIDs, value.Number)
			}
			h.Sum(hash[:0])
			dc.hashes = append(dc.hashes, *hash)
		case recSampleValue:
			// repeated int64
			switch value.WireType {
			case codec.WireBytes:
				bs := value.Bytes
				for len(bs) > 0 {
					x, n := binary.Uvarint(bs)
					values = append(values, int64(x))
					bs = bs[n:]
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
			str := uint64(0xffffffffffffffff) // sentinel for "value is numeric, not a string"
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
			h.Reset()
			h.Write(dc.stringTable.h[uint(key)][:])
			h.Write(dc.stringTable.h[uint(unit)][:])
			binary.BigEndian.PutUint64(dc.scratch[:], num)
			h.Write(dc.scratch[:8])
			if str < uint64(len(dc.stringTable.h)) {
				h.Write(dc.stringTable.h[uint(str)][:])
			}
			h.Sum(hash[:0])
			dc.hashes = append(dc.hashes, *hash)
		}
		return true, nil
	})
	sort.Sort(ByHash(dc.hashes))

	h.Reset()
	for _, sub := range dc.hashes {
		copy(hash[:], sub[:])
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
				dc.includeString[value.Number] = true
			}
		}
		return true, nil
	})
}

func (dc *DeltaComputer) keepLocations(locationIDs []uint64) {
	for _, locationID := range locationIDs {
		mappingID, functionIDs := dc.locationIndex.getMeta(locationID)
		dc.includeMapping[mappingID] = struct{}{}
		dc.includeLocation[locationID] = struct{}{}
		for _, functionID := range functionIDs {
			dc.includeFunction[functionID] = struct{}{}
		}
	}
}

type Hash [16]byte

type ByHash []Hash

func (h ByHash) Len() int           { return len(h) }
func (h ByHash) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h ByHash) Less(i, j int) bool { return bytes.Compare(h[i][:], h[j][:]) == -1 }

type Value [4]int64

type StringTable struct {
	// Passing a byte slice to hash.Hash causes it to escape to the heap, so
	// we keep around a single Hash to reuse to avoid a new allocation every
	// time we add an element to the string table
	reuse Hash
	h     []Hash
}

func (s *StringTable) Add(b []byte, h hash.Hash) {
	h.Reset()
	h.Write(b)
	h.Sum(s.reuse[:0])
	s.h = append(s.h, s.reuse)
}

// Equals returns whether the value at index i equals the byte string b
func (s *StringTable) Equals(i int, b []byte, h hash.Hash) bool {
	h.Reset()
	h.Write(b)
	h.Sum(s.reuse[:0])
	return s.reuse == s.h[i]
}
