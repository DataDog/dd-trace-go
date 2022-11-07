package fastdelta

import "hash"

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
