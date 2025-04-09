// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package filebitmap

import (
	"testing"
)

// TestConstructorWithSizeCreatesEmptyBitmap verifies that a new bitmap (created from a byte slice)
// has the expected size and that all bits are initially false.
func TestConstructorWithSizeCreatesEmptyBitmap(t *testing.T) {
	lines := 135
	size := getSize(lines)
	bitmap := NewFileBitmapFromBytes(make([]byte, size))

	if bitmap.Size() != size {
		t.Errorf("expected size %d, got %d", size, bitmap.Size())
	}

	// Check each bit (1-indexed) is false.
	for i := 0; i < lines; i++ {
		if bitmap.Get(i + 1) {
			t.Errorf("expected bit %d to be false", i+1)
		}
	}
}

// TestSetSingleBitSetsBitCorrectly verifies that setting a single bit works.
func TestSetSingleBitSetsBitCorrectly(t *testing.T) {
	bitmap := NewFileBitmapFromBytes(make([]byte, 1))
	bitmap.Set(1) // Set the first bit
	if !bitmap.Get(1) {
		t.Errorf("expected bit 1 to be set")
	}
}

// TestCountActiveBitsNoBitsSetReturnsZero verifies that a bitmap with no bits set returns zero.
func TestCountActiveBitsNoBitsSetReturnsZero(t *testing.T) {
	bitmap := NewFileBitmapFromBytes(make([]byte, 1))
	if count := bitmap.CountActiveBits(); count != 0 {
		t.Errorf("expected 0 active bits, got %d", count)
	}
}

// TestCountActiveBitsOneBitSetReturnsOne verifies that a bitmap with one bit set returns one.
func TestCountActiveBitsOneBitSetReturnsOne(t *testing.T) {
	bitmap := NewFileBitmapFromBytes(make([]byte, 1))
	bitmap.Set(1)
	if count := bitmap.CountActiveBits(); count != 1 {
		t.Errorf("expected 1 active bit, got %d", count)
	}
}

// TestBitwiseOrTwoBitmapsCombinesCorrectly verifies the OR operation on two bitmaps.
func TestBitwiseOrTwoBitmapsCombinesCorrectly(t *testing.T) {
	bitmapA := NewFileBitmapFromBytes([]byte{0b00000001})
	bitmapB := NewFileBitmapFromBytes([]byte{0b00000010})
	resultBitmap := Or(bitmapA, bitmapB, false)
	result := resultBitmap.ToArray()
	if result[0] != 0b00000011 {
		t.Errorf("expected 0b00000011, got %08b", result[0])
	}
}

// TestBitwiseAndTwoBitmapsIntersectsCorrectly verifies the AND operation on two bitmaps.
func TestBitwiseAndTwoBitmapsIntersectsCorrectly(t *testing.T) {
	bitmapA := NewFileBitmapFromBytes([]byte{0b00000011})
	bitmapB := NewFileBitmapFromBytes([]byte{0b00000010})
	resultBitmap := And(bitmapA, bitmapB, false)
	result := resultBitmap.ToArray()
	if result[0] != 0b00000010 {
		t.Errorf("expected 0b00000010, got %08b", result[0])
	}
}

// TestBitwiseNotSingleBitmapInvertsCorrectly verifies that the NOT operation correctly inverts the bits.
func TestBitwiseNotSingleBitmapInvertsCorrectly(t *testing.T) {
	bitmap := NewFileBitmapFromBytes([]byte{0b11111110})
	resultBitmap := Not(bitmap, false)
	result := resultBitmap.ToArray()
	if result[0] != 0b00000001 {
		t.Errorf("expected 0b00000001, got %08b", result[0])
	}
}

// TestLargeBitmapBitwiseOperationsHandleCorrectly verifies bitwise operations on larger bitmaps.
func TestLargeBitmapBitwiseOperationsHandleCorrectly(t *testing.T) {
	size := 1024 // 1 KB in bytes
	bitmapA := NewFileBitmapFromBytes(make([]byte, size))
	bitmapB := NewFileBitmapFromBytes(make([]byte, size))
	totalBits := size * 8

	// Set alternating bits in bitmapA and bitmapB with shifted positions.
	for i := 1; i <= totalBits; i += 2 {
		bitmapA.Set(i)
		if i+1 <= totalBits {
			bitmapB.Set(i + 1)
		}
	}

	resultOr := Or(bitmapA, bitmapB, false)
	resultAnd := And(bitmapA, bitmapB, false)

	if count := resultOr.CountActiveBits(); count != totalBits {
		t.Errorf("expected all %d bits set in OR result, got %d", totalBits, count)
	}
	if count := resultAnd.CountActiveBits(); count != 0 {
		t.Errorf("expected 0 bits set in AND result, got %d", count)
	}
}

// TestBitwiseNotComplexPatternInvertsCorrectly verifies that a complex pattern is inverted properly.
func TestBitwiseNotComplexPatternInvertsCorrectly(t *testing.T) {
	size := 256 // 256 bytes
	pattern := make([]byte, size)
	for i := 0; i < size; i++ {
		if i%2 == 0 {
			pattern[i] = 0xAA
		} else {
			pattern[i] = 0x55
		}
	}

	bitmap := NewFileBitmapFromBytes(pattern)
	invertedBitmap := Not(bitmap, false)
	totalBits := size * 8

	for i := 0; i < totalBits; i++ {
		originalBit := bitmap.Get(i + 1)
		invertedBit := invertedBitmap.Get(i + 1)
		if originalBit == invertedBit {
			t.Errorf("bit %d: expected inversion, got same value", i+1)
		}
	}
}

// TestToArrayWithVariousBitSetsReturnsExpectedByteArray verifies that setting various bits produces the expected byte array.
func TestToArrayWithVariousBitSetsReturnsExpectedByteArray(t *testing.T) {
	tests := []struct {
		bitsToSet []int
		expected  []byte
	}{
		{[]int{1, 8}, []byte{0b10000001, 0x00, 0x00, 0x00}},
		{[]int{9, 32}, []byte{0x00, 0b10000000, 0x00, 0b00000001}},
		{[]int{1, 8, 9, 32}, []byte{0b10000001, 0b10000000, 0x00, 0b00000001}},
	}

	for _, tt := range tests {
		bitmap := NewFileBitmapFromBytes(make([]byte, 4)) // 4 bytes = 32 bits
		for _, bit := range tt.bitsToSet {
			bitmap.Set(bit)
		}
		actual := bitmap.ToArray()
		if len(actual) != len(tt.expected) {
			t.Errorf("expected array length %d, got %d", len(tt.expected), len(actual))
		}
		for i, b := range tt.expected {
			if actual[i] != b {
				t.Errorf("at index %d, expected %08b, got %08b", i, b, actual[i])
			}
		}
	}
}

// TestEnumeratorCorrectlyIteratesOverBits verifies that iterating over the internal byte slice
// yields the correct bit states. (Note: our Go implementation does not provide a dedicated enumerator,
// so we iterate over the underlying data.)
func TestEnumeratorCorrectlyIteratesOverBits(t *testing.T) {
	tests := []struct {
		bitmapBytes       []byte
		expectedBitStates []bool
	}{
		{
			[]byte{0b10101010},
			[]bool{true, false, true, false, true, false, true, false},
		},
		{
			[]byte{0b11110000},
			[]bool{true, true, true, true, false, false, false, false},
		},
		{
			[]byte{0b00000000, 0b11111111},
			[]bool{
				false, false, false, false, false, false, false, false,
				true, true, true, true, true, true, true, true,
			},
		},
	}

	for _, tt := range tests {
		bitmap := NewFileBitmapFromBytes(tt.bitmapBytes)
		var iterated []bool
		for _, b := range bitmap.data {
			for bitPos := 0; bitPos < 8; bitPos++ {
				// Extract bit from most significant to least significant.
				bit := (b & (1 << (7 - bitPos))) != 0
				iterated = append(iterated, bit)
			}
		}
		if len(iterated) != len(tt.expectedBitStates) {
			t.Errorf("expected %d bits, got %d", len(tt.expectedBitStates), len(iterated))
		}
		for i, expected := range tt.expectedBitStates {
			if iterated[i] != expected {
				t.Errorf("bit %d: expected %v, got %v", i, expected, iterated[i])
			}
		}
	}
}

// TestGivenARangeWhenCreatingFileBitmapProperBitsAreSet verifies that FromActiveRange
// properly sets bits in a given range and panics on invalid ranges.
func TestGivenARangeWhenCreatingFileBitmapProperBitsAreSet(t *testing.T) {
	tests := []struct {
		from        int
		to          int
		shouldPanic bool
	}{
		{0, 10, true},
		{15, 10, true},
		{1, 10, false},
		{5, 15, false},
		{5, 5, false},
	}

	for _, tt := range tests {
		if tt.shouldPanic {
			didPanic := false
			func() {
				defer func() {
					if r := recover(); r != nil {
						didPanic = true
					}
				}()
				_ = FromActiveRange(tt.from, tt.to)
			}()
			if !didPanic {
				t.Errorf("expected panic for range (%d, %d)", tt.from, tt.to)
			}
		} else {
			bitmap := FromActiveRange(tt.from, tt.to)
			if bitmap.BitCount() < tt.to {
				t.Errorf("expected bitmap bit count to be >= %d, got %d", tt.to, bitmap.BitCount())
			}
			for x := 1; x <= bitmap.BitCount(); x++ {
				bit := bitmap.Get(x)
				expected := (x >= tt.from && x <= tt.to)
				if bit != expected {
					t.Errorf("bit %d: expected %v, got %v", x, expected, bit)
				}
			}
		}
	}
}

// TestGivenTwoRangesWhenIntersectingFileBitmapsResultIsExpected verifies that the
// IntersectsWith method correctly identifies overlapping ranges.
func TestGivenTwoRangesWhenIntersectingFileBitmapsResultIsExpected(t *testing.T) {
	tests := []struct {
		from1, to1 int
		from2, to2 int
		intersect  bool
	}{
		{1, 10, 11, 15, false},
		{11, 15, 1, 10, false},
		{1, 10, 8, 15, true},
		{8, 15, 10, 15, true},
		{8, 15, 15, 16, true},
		{30, 36, 35, 35, true},
	}

	for _, tt := range tests {
		bitmap1 := FromActiveRange(tt.from1, tt.to1)
		bitmap2 := FromActiveRange(tt.from2, tt.to2)
		result := bitmap1.IntersectsWith(bitmap2)
		if result != tt.intersect {
			t.Errorf("Intersection of range (%d, %d) and (%d, %d): expected %v, got %v",
				tt.from1, tt.to1, tt.from2, tt.to2, tt.intersect, result)
		}
	}
}
