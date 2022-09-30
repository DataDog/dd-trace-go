package fastdelta

// locationIndex links location IDs to the addresses, mappings, and function
// IDs referenced by the location
type locationIndex struct {
	// fastpath bookkeeping when locations have 1-up identifiers
	fastAddress     []uint64
	fastMappingId   []uint64
	fastFunctionIds [][]uint64
	fastFunctionIdx int

	fallbackAddress     map[uint64]uint64
	fallbackMappingId   map[uint64]uint64
	fallbackFunctionIds map[uint64][]uint64
}

func (l *locationIndex) reset() {
	l.fastAddress = l.fastAddress[:0]
	l.fastMappingId = l.fastMappingId[:0]
	l.fastFunctionIdx = 0

	l.fallbackAddress = nil
	l.fallbackMappingId = nil
	l.fallbackFunctionIds = nil
}

// insert associates the given address, mapping ID, and function IDs with the
// given location ID
func (l *locationIndex) insert(id, address, mappingId uint64, functionIds []uint64) {
	if l.fallbackAddress != nil {
		// we're already on the slow path
		l.insertSlow(id, address, mappingId, functionIds)
		return
	}
	if id != uint64(len(l.fastAddress)+1) {
		// Locations don't have 1-up IDs, we have to fall back to a map
		l.fallback()
		l.insertSlow(id, address, mappingId, functionIds)
		return
	}
	l.insertFast(address, mappingId, functionIds)
}

func (l *locationIndex) insertFast(address uint64, mappingId uint64, functionIds []uint64) {
	l.fastAddress = append(l.fastAddress, address)
	l.fastMappingId = append(l.fastMappingId, mappingId)

	// re-use [][]uint64 allocations
	if len(l.fastFunctionIds) > l.fastFunctionIdx {
		l.fastFunctionIds[l.fastFunctionIdx] = l.fastFunctionIds[l.fastFunctionIdx][:0]
	} else {
		l.fastFunctionIds = append(l.fastFunctionIds, make([]uint64, 0, len(functionIds)))
	}
	l.fastFunctionIds[l.fastFunctionIdx] = append(l.fastFunctionIds[l.fastFunctionIdx], functionIds...)
	l.fastFunctionIdx++
}

func (l *locationIndex) insertSlow(id uint64, address uint64, mappingId uint64, functionIds []uint64) {
	l.fallbackAddress[id] = address
	l.fallbackMappingId[id] = mappingId
	l.fallbackFunctionIds[id] = append(l.fallbackFunctionIds[id], functionIds...)
}

// fallback moves location data from the faster storage (when IDs are assigned
// 1-up) to the slower storage
func (l *locationIndex) fallback() {
	l.fallbackAddress = make(map[uint64]uint64)
	l.fallbackMappingId = make(map[uint64]uint64)
	l.fallbackFunctionIds = make(map[uint64][]uint64)
	for i, addr := range l.fastAddress {
		id := uint64(i) + 1
		l.fallbackAddress[id] = addr
		l.fallbackMappingId[id] = l.fastMappingId[i]
		l.fallbackFunctionIds[id] = l.fastFunctionIds[i]
	}

	l.fastAddress = l.fastAddress[:0]
	l.fastMappingId = l.fastMappingId[:0]
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
func (l *locationIndex) getMeta(id uint64) (mappingId uint64, functionIds []uint64) {
	if l.fallbackAddress != nil {
		mappingId = l.fallbackMappingId[id]
		functionIds = l.fallbackFunctionIds[id]
		return
	}
	mappingId = l.fastMappingId[uint(id-1)]
	functionIds = l.fastFunctionIds[uint(id-1)]
	return
}
