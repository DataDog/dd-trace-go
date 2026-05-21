// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"runtime"
	"slices"
	"time"

	"github.com/DataDog/dd-trace-go/v2/profiler/internal/pproflite"
)

// collectCappedProfile returns a capped pprof for pt if a stack cap is
// configured, or (nil, nil) if no cap is set for that profile type.
func collectCappedProfile(cfg *config, pt ProfileType) ([]byte, error) {
	switch pt {
	case HeapProfile:
		if cfg.maxHeapStacks > 0 {
			return buildCappedHeapPprof(cfg.maxHeapStacks)
		}
	case MutexProfile:
		if cfg.maxMutexStacks > 0 {
			return buildCappedMutexPprof(cfg.maxMutexStacks)
		}
	case BlockProfile:
		if cfg.maxBlockStacks > 0 {
			return buildCappedBlockPprof(cfg.maxBlockStacks)
		}
	}
	return nil, nil
}

// applyDeltaOrCompress applies delta profiling (if enabled) or compresses raw
// pprof bytes using the configured compressor for the given profile type.
func applyDeltaOrCompress(p *profiler, pt ProfileType, name string, raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	dp, ok := p.deltas[pt]
	if !ok || !p.cfg.deltaProfiles {
		c := p.compressors[pt]
		c.Reset(&buf)
		if _, err := c.Write(raw); err != nil {
			c.Close()
			return nil, err
		}
		if err := c.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	start := time.Now()
	delta, err := dp.Delta(raw)
	tags := append(p.cfg.tags.Slice(), fmt.Sprintf("profile_type:%s", name))
	p.cfg.statsd.Timing("datadog.profiling.go.delta_time", time.Since(start), tags, 1)
	if err != nil {
		return nil, fmt.Errorf("delta profile error: %s", err.Error())
	}
	return delta, err
}

// buildCappedHeapPprof collects all MemProfileRecords, keeps only the top
// maxStacks by InUseBytes, and serializes to uncompressed pprof protobuf.
func buildCappedHeapPprof(maxStacks int) ([]byte, error) {
	var records []runtime.MemProfileRecord
	for {
		n, ok := runtime.MemProfile(records, true)
		if ok {
			records = records[:n]
			break
		}
		records = make([]runtime.MemProfileRecord, n+10)
	}
	slices.SortFunc(records, func(a, b runtime.MemProfileRecord) int {
		return cmp.Compare(b.InUseBytes(), a.InUseBytes())
	})
	if len(records) > maxStacks {
		records = records[:maxStacks]
	}
	return encodeHeapPprof(records)
}

// buildCappedMutexPprof collects all mutex BlockProfileRecords, keeps only the
// top maxStacks by Cycles, and serializes to uncompressed pprof protobuf.
func buildCappedMutexPprof(maxStacks int) ([]byte, error) {
	return buildCappedContentionPprof(maxStacks, runtime.MutexProfile)
}

// buildCappedBlockPprof collects all block BlockProfileRecords, keeps only the
// top maxStacks by Cycles, and serializes to uncompressed pprof protobuf.
func buildCappedBlockPprof(maxStacks int) ([]byte, error) {
	return buildCappedContentionPprof(maxStacks, runtime.BlockProfile)
}

// buildCappedContentionPprof is the shared implementation for mutex and block
// profiles, which both use runtime.BlockProfileRecord sorted by Cycles.
func buildCappedContentionPprof(maxStacks int, collect func([]runtime.BlockProfileRecord) (int, bool)) ([]byte, error) {
	var records []runtime.BlockProfileRecord
	for {
		n, ok := collect(records)
		if ok {
			records = records[:n]
			break
		}
		records = make([]runtime.BlockProfileRecord, n+10)
	}
	slices.SortFunc(records, func(a, b runtime.BlockProfileRecord) int {
		return cmp.Compare(b.Cycles, a.Cycles)
	})
	if len(records) > maxStacks {
		records = records[:maxStacks]
	}
	return encodeContentionPprof(records)
}

// encodeHeapPprof serializes MemProfileRecords to uncompressed pprof protobuf.
// Sample types match the standard Go heap profile:
// alloc_objects/count, alloc_space/bytes, inuse_objects/count, inuse_space/bytes.
func encodeHeapPprof(records []runtime.MemProfileRecord) ([]byte, error) {
	st := newStringTable()
	aoType, countUnit := st.intern("alloc_objects"), st.intern("count")
	asType, bytesUnit := st.intern("alloc_space"), st.intern("bytes")
	ioType := st.intern("inuse_objects")
	isType := st.intern("inuse_space")

	sampleTypes := []pproflite.SampleType{
		{ValueType: pproflite.ValueType{Type: aoType, Unit: countUnit}},
		{ValueType: pproflite.ValueType{Type: asType, Unit: bytesUnit}},
		{ValueType: pproflite.ValueType{Type: ioType, Unit: countUnit}},
		{ValueType: pproflite.ValueType{Type: isType, Unit: bytesUnit}},
	}

	b := newProfBuilder(st)
	samples := make([]pproflite.Sample, 0, len(records))
	for i := range records {
		rec := &records[i]
		if stack := rec.Stack(); len(stack) > 0 {
			samples = append(samples, pproflite.Sample{
				LocationID: b.resolveStack(stack),
				Value:      []int64{rec.AllocObjects, rec.AllocBytes, rec.InUseObjects(), rec.InUseBytes()},
			})
		}
	}

	var buf bytes.Buffer
	err := b.encode(&buf, sampleTypes, samples)
	return buf.Bytes(), err
}

// encodeContentionPprof serializes BlockProfileRecords to uncompressed pprof
// protobuf. Used for both mutex and block profiles.
// Sample types: contentions/count, delay/nanoseconds.
// Note: Cycles is stored directly as the delay value without converting to
// nanoseconds, which is sufficient for delta computation and relative ordering.
func encodeContentionPprof(records []runtime.BlockProfileRecord) ([]byte, error) {
	st := newStringTable()
	contentionsType, countUnit := st.intern("contentions"), st.intern("count")
	delayType, nanosUnit := st.intern("delay"), st.intern("nanoseconds")

	sampleTypes := []pproflite.SampleType{
		{ValueType: pproflite.ValueType{Type: contentionsType, Unit: countUnit}},
		{ValueType: pproflite.ValueType{Type: delayType, Unit: nanosUnit}},
	}

	b := newProfBuilder(st)
	samples := make([]pproflite.Sample, 0, len(records))
	for i := range records {
		rec := &records[i]
		if stack := rec.Stack(); len(stack) > 0 {
			samples = append(samples, pproflite.Sample{
				LocationID: b.resolveStack(stack),
				Value:      []int64{rec.Count, rec.Cycles},
			})
		}
	}

	var buf bytes.Buffer
	err := b.encode(&buf, sampleTypes, samples)
	return buf.Bytes(), err
}

// stringTable maps strings to their index in the pprof string table.
// Index 0 is always the empty string.
type stringTable struct {
	strs []string
	idx  map[string]int64
}

func newStringTable() *stringTable {
	return &stringTable{
		strs: []string{""},
		idx:  map[string]int64{"": 0},
	}
}

func (st *stringTable) intern(s string) int64 {
	if i, ok := st.idx[s]; ok {
		return i
	}
	i := int64(len(st.strs))
	st.strs = append(st.strs, s)
	st.idx[s] = i
	return i
}

// profBuilder accumulates locations and functions while resolving symbols via
// runtime.CallersFrames. Each unique PC is resolved at most once.
type profBuilder struct {
	st        *stringTable
	locs      []pproflite.Location
	fns       []pproflite.Function
	locByPC   map[uintptr]uint64 // PC -> location ID
	fnByKey   map[string]uint64  // "name\x00file" -> function ID
	nextLocID uint64
	nextFnID  uint64
}

func newProfBuilder(st *stringTable) *profBuilder {
	return &profBuilder{
		st:        st,
		locByPC:   make(map[uintptr]uint64),
		fnByKey:   make(map[string]uint64),
		nextLocID: 1,
		nextFnID:  1,
	}
}

// resolveStack returns location IDs for the given PCs, resolving symbols for
// any PC not previously seen. Inlined frames produce multiple lines per location.
func (b *profBuilder) resolveStack(pcs []uintptr) []uint64 {
	locIDs := make([]uint64, 0, len(pcs))
	for _, pc := range pcs {
		if locID, ok := b.locByPC[pc]; ok {
			locIDs = append(locIDs, locID)
			continue
		}
		frames := runtime.CallersFrames([]uintptr{pc})
		var lines []pproflite.Line
		for {
			f, more := frames.Next()
			fnKey := f.Function + "\x00" + f.File
			fnID, ok := b.fnByKey[fnKey]
			if !ok {
				fnID = b.nextFnID
				b.nextFnID++
				b.fnByKey[fnKey] = fnID
				b.fns = append(b.fns, pproflite.Function{
					ID:         fnID,
					Name:       b.st.intern(f.Function),
					SystemName: b.st.intern(f.Function),
					FileName:   b.st.intern(f.File),
				})
			}
			lines = append(lines, pproflite.Line{FunctionID: fnID, Line: int64(f.Line)})
			if !more {
				break
			}
		}
		locID := b.nextLocID
		b.nextLocID++
		b.locByPC[pc] = locID
		b.locs = append(b.locs, pproflite.Location{ID: locID, Address: uint64(pc), Line: lines})
		locIDs = append(locIDs, locID)
	}
	return locIDs
}

// encode writes the profile to w as an uncompressed pprof protobuf message.
func (b *profBuilder) encode(w io.Writer, sampleTypes []pproflite.SampleType, samples []pproflite.Sample) error {
	enc := pproflite.NewEncoder(w)
	for i := range sampleTypes {
		if err := enc.Encode(&sampleTypes[i]); err != nil {
			return fmt.Errorf("encoding sample type: %w", err)
		}
	}
	for i := range samples {
		if err := enc.Encode(&samples[i]); err != nil {
			return fmt.Errorf("encoding sample: %w", err)
		}
	}
	for i := range b.locs {
		if err := enc.Encode(&b.locs[i]); err != nil {
			return fmt.Errorf("encoding location: %w", err)
		}
	}
	for i := range b.fns {
		if err := enc.Encode(&b.fns[i]); err != nil {
			return fmt.Errorf("encoding function: %w", err)
		}
	}
	for _, s := range b.st.strs {
		if err := enc.Encode(&pproflite.StringTable{Value: []byte(s)}); err != nil {
			return fmt.Errorf("encoding string table: %w", err)
		}
	}
	return enc.Encode(&pproflite.TimeNanos{Value: time.Now().UnixNano()})
}
