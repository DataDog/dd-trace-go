// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package json

import (
	"reflect"
	"runtime"
	"unsafe"

	jsoniter "github.com/json-iterator/go"
)

const intSize = unsafe.Sizeof(int(0))

var (
	headOffset  uintptr
	tailOffset  uintptr
	depthOffset uintptr
)

func getOffset(name string, store *uintptr) {
	typ := reflect.TypeFor[jsoniter.Iterator]()
	field, found := typ.FieldByName(name)
	if !found {
		panic("jsoniter.Iterator does not have a field named '" + name + "'")
	}

	if field.Type.Size() != intSize {
		panic("jsoniter.Iterator field '" + name + "' is not of the right size")
	}

	*store = field.Offset
}

func init() {
	getOffset("head", &headOffset)
	getOffset("tail", &tailOffset)
	getOffset("depth", &depthOffset)
}

func getIteratorHeadTailAndDepth(iter *jsoniter.Iterator) (head, tail, depth int) {
	head, tail, depth = *(*int)(unsafe.Add(unsafe.Pointer(iter), headOffset)),
		*(*int)(unsafe.Add(unsafe.Pointer(iter), tailOffset)),
		*(*int)(unsafe.Add(unsafe.Pointer(iter), depthOffset))

	runtime.KeepAlive(iter) // Ensure the iterator is not garbage collected while we're using it
	return head, tail, depth
}

// setIteratorHeadAndDepth sets the head and depth of the jsoniter iterator
func setIteratorHeadAndDepth(iter *jsoniter.Iterator, head, depth int) {
	*(*int)(unsafe.Add(unsafe.Pointer(iter), headOffset)) = head
	*(*int)(unsafe.Add(unsafe.Pointer(iter), depthOffset)) = depth
	runtime.KeepAlive(iter) // Ensure the iterator is not garbage collected while we're using it
}
