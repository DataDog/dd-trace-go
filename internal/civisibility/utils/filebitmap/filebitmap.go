// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package filebitmap

import (
	"fmt"
	"math/bits"
)

// FileBitmap represents a memory-efficient, modifiable bitmap.
type FileBitmap struct {
	data []byte
}

// NewFileBitmapFromBytes creates a new FileBitmap from a given byte slice without modifying it.
func NewFileBitmapFromBytes(data []byte) *FileBitmap {
	if data == nil {
		panic("bitmap array source is nil")
	}
	return &FileBitmap{data: data}
}

// FromLineCount creates a new FileBitmap that can hold the specified number of lines (bits).
func FromLineCount(lines int) *FileBitmap {
	size := getSize(lines)
	return &FileBitmap{data: make([]byte, size)}
}

// FromActiveRange creates a FileBitmap with enough space for 'toLine' lines,
// and sets all bits in the range [fromLine, toLine] (1-indexed).
func FromActiveRange(fromLine, toLine int) *FileBitmap {
	if fromLine <= 0 || toLine < fromLine {
		panic("Invalid range")
	}
	fb := FromLineCount(toLine)
	for i := fromLine; i <= toLine; i++ {
		fb.Set(i)
	}
	return fb
}

// getSize returns the number of bytes needed for numOfLines bits.
func getSize(numOfLines int) int {
	return (numOfLines + 7) / 8
}

// Size returns the size of the bitmap in bytes.
func (fb *FileBitmap) Size() int {
	return len(fb.data)
}

// BitCount returns the total number of bits in the bitmap.
func (fb *FileBitmap) BitCount() int {
	return len(fb.data) * 8
}

// Set sets the bit at the given line (1-indexed) to 1.
func (fb *FileBitmap) Set(line int) {
	if fb.data == nil {
		return
	}
	idx := line - 1      // adjust for zero-based index
	byteIndex := idx / 8 // each byte holds 8 bits
	if byteIndex >= len(fb.data) {
		panic("line out of range")
	}
	bitMask := byte(128 >> (idx % 8)) // 128 >> (idx mod 8) creates the proper mask
	fb.data[byteIndex] |= bitMask
}

// Get returns true if the bit at the given line (1-indexed) is set to 1.
func (fb *FileBitmap) Get(line int) bool {
	if fb.data == nil {
		return false
	}
	idx := line - 1
	byteIndex := idx / 8
	if byteIndex >= len(fb.data) {
		return false
	}
	bitMask := byte(128 >> (idx % 8))
	return (fb.data[byteIndex] & bitMask) != 0
}

// CountActiveBits counts the number of bits set to 1 in the bitmap.
func (fb *FileBitmap) CountActiveBits() int {
	count := 0
	// Use the math/bits package to count the 1 bits in each byte.
	for _, b := range fb.data {
		count += bits.OnesCount8(b)
	}
	return count
}

// HasActiveBits returns true if at least one bit is set in the bitmap.
func (fb *FileBitmap) HasActiveBits() bool {
	for _, b := range fb.data {
		if b != 0 {
			return true
		}
	}
	return false
}

// IntersectsWith returns true if this bitmap has at least one common set bit with the other bitmap.
func (fb *FileBitmap) IntersectsWith(other *FileBitmap) bool {
	minSize := len(fb.data)
	if len(other.data) < minSize {
		minSize = len(other.data)
	}
	for i := 0; i < minSize; i++ {
		if (fb.data[i] & other.data[i]) != 0 {
			return true
		}
	}
	return false
}

// Or performs a bitwise OR operation between two FileBitmaps.
// If reuseBuffer is true, the result is stored in bitmap 'a'; otherwise, a new FileBitmap is allocated.
func Or(a, b *FileBitmap, reuseBuffer bool) *FileBitmap {
	var minSize, maxSize int
	if len(a.data) < len(b.data) {
		minSize = len(a.data)
		maxSize = len(b.data)
	} else {
		minSize = len(b.data)
		maxSize = len(a.data)
	}

	var res *FileBitmap
	if reuseBuffer {
		res = a
	} else {
		res = &FileBitmap{data: make([]byte, maxSize)}
	}

	// Perform bitwise OR on the overlapping region.
	for i := 0; i < minSize; i++ {
		res.data[i] = a.data[i] | b.data[i]
	}

	// If the sizes differ, copy the remaining bytes from the larger bitmap.
	if len(a.data) != len(b.data) {
		if len(a.data) > len(b.data) {
			for i := minSize; i < maxSize; i++ {
				res.data[i] = a.data[i]
			}
		} else {
			for i := minSize; i < maxSize; i++ {
				res.data[i] = b.data[i]
			}
		}
	}

	return res
}

// And performs a bitwise AND operation between two FileBitmaps.
// If reuseBuffer is true, the result is stored in bitmap 'a'; otherwise, a new FileBitmap is allocated.
func And(a, b *FileBitmap, reuseBuffer bool) *FileBitmap {
	var minSize, maxSize int
	if len(a.data) < len(b.data) {
		minSize = len(a.data)
		maxSize = len(b.data)
	} else {
		minSize = len(b.data)
		maxSize = len(a.data)
	}

	var res *FileBitmap
	if reuseBuffer {
		res = a
	} else {
		res = &FileBitmap{data: make([]byte, maxSize)}
	}

	// Perform bitwise AND on the overlapping region.
	for i := 0; i < minSize; i++ {
		res.data[i] = a.data[i] & b.data[i]
	}
	// For the remaining bytes (if any), fill with 0.
	for i := minSize; i < maxSize; i++ {
		res.data[i] = 0
	}
	return res
}

// Not performs a bitwise NOT operation on a FileBitmap.
// If reuseBuffer is true, the result is stored in the original bitmap; otherwise, a new FileBitmap is allocated.
func Not(a *FileBitmap, reuseBuffer bool) *FileBitmap {
	var res *FileBitmap
	if reuseBuffer {
		res = a
	} else {
		res = &FileBitmap{data: make([]byte, len(a.data))}
	}
	for i, b := range a.data {
		res.data[i] = ^b
	}
	return res
}

// ToArray returns a new copy of the bitmap data.
func (fb *FileBitmap) ToArray() []byte {
	dst := make([]byte, len(fb.data))
	copy(dst, fb.data)
	return dst
}

// GetBuffer returns the internal byte buffer of the bitmap.
func (fb *FileBitmap) GetBuffer() []byte {
	return fb.data
}

// String returns a string representation of the bitmap as a binary string.
func (fb *FileBitmap) String() string {
	s := ""
	for _, b := range fb.data {
		s += fmt.Sprintf("%08b", b)
	}
	return s
}
