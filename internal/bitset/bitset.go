// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package bitset implements a simple binary data structure that acts as a set, along with operations for retrieving and storing data.
package bitset

import (
	"bytes"
	"math/bits"
	"strconv"
)

const (
	wordLength = uint(64) // number of bits that are represented in an address array
	n          = uint(6)  // shift value based on word length
	maxBits    = ^uint(0) // total possible number of bits
)

// BitSet is an implementation of an array structure that exclusively maps non-negative integers and boolean values.
// Code inspired from https://github.com/willf/bitset/blob/master/bitset.go.
type BitSet struct {
	length uint
	data   []uint64
}

// New initializes a new BitSet with a given length.
func New(length uint) (b *BitSet) {
	defer func() {
		if r := recover(); r != nil {
			b = &BitSet{
				0,
				make([]uint64, 0),
			}
		}
	}()

	b = &BitSet{
		length,
		make([]uint64, wordsNeeded(length)),
	}

	return b
}

// Contains returns whether the bitset contains the bit value.
func (b *BitSet) Contains(i uint) bool {
	if i >= b.length {
		return false
	}
	return b.data[i>>n]&(1<<(i&(wordLength-1))) != 0
}

// Add adds the value to the bitset and returns the bitset with an increased capacity.
func (b *BitSet) Add(i uint) *BitSet {
	b.extendSet(i)
	b.data[i>>n] |= 1 << (i & (wordLength - 1))
	return b
}

// GetData retrieves the items of the bitset.
func (b *BitSet) GetData() []uint64 {
	return b.data
}

// String represents BitSet data as a string.
func (b *BitSet) String() string {
	var buffer bytes.Buffer
	start := []byte("{")
	buffer.Write(start)
	counter := 0
	i, e := b.nextItem(0)
	for e {
		counter = counter + 1
		// to avoid exhausting the memory
		if counter > 0x40000 {
			buffer.WriteString("...")
			break
		}
		buffer.WriteString(strconv.FormatInt(int64(i), 10))
		i, e = b.nextItem(i + 1)
		if e {
			buffer.WriteString(", ")
		}
	}
	buffer.WriteString("}")
	return buffer.String()
}

// nextItem returns the next member of the bitset from the specified index.
func (b *BitSet) nextItem(i uint) (uint, bool) {
	x := int(i >> n)
	if x >= len(b.data) {
		return 0, false
	}
	w := b.data[x]
	w = w >> (i & (wordLength - 1))
	if w != 0 {
		return i + uint(bits.TrailingZeros64(w)), true
	}
	x = x + 1
	for x < len(b.data) {
		if b.data[x] != 0 {
			return uint(x)*wordLength + uint(bits.TrailingZeros64(b.data[x])), true
		}
		x = x + 1
	}
	return 0, false
}

// extendSet adds additional words to incorporate new bits
func (b *BitSet) extendSet(i uint) {
	if i >= b.length {
		if i >= maxBits {
			panic("Exceeded max bitset capacity")
		}
		nsize := wordsNeeded(i + 1)
		if b.data == nil {
			b.data = make([]uint64, nsize)
		} else if cap(b.data) >= nsize {
			b.data = b.data[:nsize] // fast resize
		} else if len(b.data) < nsize {
			newset := make([]uint64, nsize, 2*nsize) // double capacity
			copy(newset, b.data)
			b.data = newset
		}
		b.length = i + 1
	}
}

// wordsNeeded calculates the number of words needed for i bits
func wordsNeeded(i uint) int {
	if i > (maxBits - wordLength + 1) {
		return int(maxBits >> n)
	}
	return int((i + (wordLength - 1)) >> n)
}
