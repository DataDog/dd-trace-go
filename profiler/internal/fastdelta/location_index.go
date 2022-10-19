// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package fastdelta

// locationIndex links location IDs to the addresses, mappings, and function
// IDs referenced by the location
type locationIndex struct {
	// fastpath bookkeeping when locations have 1-up identifiers
	fastAddress     []uint64
	fastMappingID   []uint64
	fastFunctionIDs [][]uint64
	fastFunctionIdx int

	fallbackAddress     map[uint64]uint64
	fallbackMappingID   map[uint64]uint64
	fallbackFunctionIDs map[uint64][]uint64
}

func (l *locationIndex) reset() {
	l.fastAddress = l.fastAddress[:0]
	l.fastMappingID = l.fastMappingID[:0]
	l.fastFunctionIdx = 0

	l.fallbackAddress = nil
	l.fallbackMappingID = nil
	l.fallbackFunctionIDs = nil
}

// insert associates the given address, mapping ID, and function IDs with the
// given location ID
func (l *locationIndex) insert(id, address, mappingID uint64, functionIDs []uint64) {
	if l.fallbackAddress != nil {
		// we're already on the slow path
		l.insertSlow(id, address, mappingID, functionIDs)
		return
	}
	if id != uint64(len(l.fastAddress)+1) {
		// Locations don't have 1-up IDs, we have to fall back to a map
		l.fallback()
		l.insertSlow(id, address, mappingID, functionIDs)
		return
	}
	l.insertFast(address, mappingID, functionIDs)
}

func (l *locationIndex) insertFast(address uint64, mappingID uint64, functionIDs []uint64) {
	l.fastAddress = append(l.fastAddress, address)
	l.fastMappingID = append(l.fastMappingID, mappingID)

	// re-use [][]uint64 allocations
	if len(l.fastFunctionIDs) > l.fastFunctionIdx {
		l.fastFunctionIDs[l.fastFunctionIdx] = l.fastFunctionIDs[l.fastFunctionIdx][:0]
	} else {
		l.fastFunctionIDs = append(l.fastFunctionIDs, make([]uint64, 0, len(functionIDs)))
	}
	l.fastFunctionIDs[l.fastFunctionIdx] = append(l.fastFunctionIDs[l.fastFunctionIdx], functionIDs...)
	l.fastFunctionIdx++
}

func (l *locationIndex) insertSlow(id uint64, address uint64, mappingID uint64, functionIDs []uint64) {
	l.fallbackAddress[id] = address
	l.fallbackMappingID[id] = mappingID
	l.fallbackFunctionIDs[id] = append(l.fallbackFunctionIDs[id], functionIDs...)
}

// fallback moves location data from the faster storage (when IDs are assigned
// 1-up) to the slower storage
func (l *locationIndex) fallback() {
	l.fallbackAddress = make(map[uint64]uint64)
	l.fallbackMappingID = make(map[uint64]uint64)
	l.fallbackFunctionIDs = make(map[uint64][]uint64)
	for i, addr := range l.fastAddress {
		id := uint64(i) + 1
		l.fallbackAddress[id] = addr
		l.fallbackMappingID[id] = l.fastMappingID[i]
		l.fallbackFunctionIDs[id] = l.fastFunctionIDs[i]
	}

	l.fastAddress = l.fastAddress[:0]
	l.fastMappingID = l.fastMappingID[:0]
	l.fastFunctionIdx = 0
}

// get returns the address associated with the given location ID
func (l *locationIndex) get(id uint64) uint64 {
	if l.fallbackAddress != nil {
		return l.fallbackAddress[id]
	}
	return l.fastAddress[uint(id-1)]
}

// getMeta returns the mapping ID and function IDs associated with
// the given location ID
func (l *locationIndex) getMeta(id uint64) (mappingID uint64, functionIDs []uint64) {
	if l.fallbackAddress != nil {
		mappingID = l.fallbackMappingID[id]
		functionIDs = l.fallbackFunctionIDs[id]
		return
	}
	mappingID = l.fastMappingID[uint(id-1)]
	functionIDs = l.fastFunctionIDs[uint(id-1)]
	return
}
