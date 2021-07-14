// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package bitset

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewBitSet(t *testing.T) {
	b := New(0)
	if b.Contains(1) {
		t.Errorf("Zero-length bitset should not contain this value")
	}
}

func TestClearBitSet(t *testing.T) {
	b := New(1000)
	for i := uint(0); i < 1000; i++ {
		if b.Contains(i) {
			t.Errorf("Bit %d is set and should be clear", i)
		}
	}
}

func TestAdd(t *testing.T) {
	var b BitSet
	b.Add(8)
	assert.Equal(t, 9, int(b.length))
	assert.Equal(t, 1, len(b.data))
	if !b.Contains(8) {
		t.Errorf("Expected bit %d to be found, but was not there", 8)
	}
}

func TestZeroValueBitSet(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Error("Zero-length bitset")
		}
	}()
	var b BitSet
	if b.length != 0 {
		t.Errorf("Empty set should have capacity 0, not %d", b.length)
	}
}

func TestEmptyBitSet(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Error("Zero-length bitset")
		}
	}()
	b := New(0)
	if b.length != 0 {
		t.Errorf("Empty set should have capacity 0, not %d", b.length)
	}
}

func TestExtendPastBounds(t *testing.T) {
	b := New(32)
	defer func() {
		if r := recover(); r != nil {
			t.Error("Border out of index error should not have caused a panic")
		}
	}()
	b.Add(32)
}

func TestExtendPastCapacity(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Set to capacity should have caused a panic")
		}
	}()
	b := New(uint(32768))
	b.Add(^uint(0))
}

func TestChain(t *testing.T) {
	b := New(1000)
	b.Add(100).Add(99)
	assert.Equal(t, 1000, int(b.length))
	assert.Equal(t, 16, len(b.data))
	if !b.Contains(100) {
		t.Errorf("Expected bit %d to be found, but was not there", 100)
	}
}

func TestNullSet(t *testing.T) {
	var b *BitSet
	defer func() {
		if r := recover(); r == nil {
			t.Error("Checking bit of null reference should have caused a panic")
		}
	}()
	b.Contains(111)
}

func TestWordsNeededLong(t *testing.T) {
	out := wordsNeeded(^uint(0))
	if out <= 0 {
		t.Error("Unexpected value: ", out)
		return
	}
}

func TestGetDataEmpty(t *testing.T) {
	b := new(BitSet)
	c := b.GetData()
	outType := fmt.Sprintf("%T", c)
	expType := "[]uint64"
	if outType != expType {
		t.Error("Expecting type: ", expType, ", gotf:", outType)
		return
	}
	if len(c) != 0 {
		t.Error("The slice was not empty but should be")
		return
	}
}

func TestGetData(t *testing.T) {
	b := new(BitSet)
	b.Add(400)
	c := b.GetData()
	if len(c) == 0 {
		t.Error("The slice is empty but should not be")
		return
	}
	assert.Equal(t, []uint64{0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x10000}, c)
}

func TestString(t *testing.T) {
	b := New(0)
	for i := uint(0); i < 10; i++ {
		b.Add(i)
	}
	assert.Equal(t, "{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}", b.String())
}

func TestStringLong(t *testing.T) {
	b := New(0)
	for i := uint(0); i < 262145; i++ {
		b.Add(i)
	}
	str := b.String()
	assert.Equal(t, 1986047, len(str))
}

func TestNextItem(t *testing.T) {
	b := New(10000)
	b.Add(0)
	b.Add(1)
	b.Add(2)
	data := make([]uint, 3)
	c := 0
	for i, e := b.nextItem(0); e; i, e = b.nextItem(i + 1) {
		data[c] = i
		c++
	}
	if data[0] != 0 {
		t.Errorf("bug 0")
	}
	if data[1] != 1 {
		t.Errorf("bug 1")
	}
	if data[2] != 2 {
		t.Errorf("bug 2")
	}
	b.Add(10)
	b.Add(2000)
	data = make([]uint, 5)
	c = 0
	for i, e := b.nextItem(0); e; i, e = b.nextItem(i + 1) {
		data[c] = i
		c++
	}
	if data[0] != 0 {
		t.Errorf("bug 0")
	}
	if data[1] != 1 {
		t.Errorf("bug 1")
	}
	if data[2] != 2 {
		t.Errorf("bug 2")
	}
	if data[3] != 10 {
		t.Errorf("bug 3")
	}
	if data[4] != 2000 {
		t.Errorf("bug 4")
	}
}
