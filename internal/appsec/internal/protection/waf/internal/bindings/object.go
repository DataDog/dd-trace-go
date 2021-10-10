// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !without_ddwaf && cgo && !windows && amd64 && (linux || darwin)
// +build !without_ddwaf
// +build cgo
// +build !windows
// +build amd64
// +build linux darwin

package bindings

import (
	"sync/atomic"
	"unsafe"

	"github.com/pkg/errors"
)

// #include <stdlib.h>
// #include "ddwaf.h"
import "C"

const (
	wafUintType    = C.DDWAF_OBJ_UNSIGNED
	wafIntType     = C.DDWAF_OBJ_SIGNED
	wafStringType  = C.DDWAF_OBJ_STRING
	wafArrayType   = C.DDWAF_OBJ_ARRAY
	wafMapType     = C.DDWAF_OBJ_MAP
	wafInvalidType = C.DDWAF_OBJ_INVALID
)

// wafObject is a Go wrapper allowing to create, access and destroy a WAF object C structure.
type wafObject C.ddwaf_object

func (v *wafObject) ctype() *C.ddwaf_object { return (*C.ddwaf_object)(v) }

// Return the pointer to the union field. It can be cast to the union type that needs to be accessed.
func (v *wafObject) valuePtr() unsafe.Pointer        { return unsafe.Pointer(&v.anon0[0]) }
func (v *wafObject) arrayValuePtr() **C.ddwaf_object { return (**C.ddwaf_object)(v.valuePtr()) }
func (v *wafObject) int64ValuePtr() *C.int64_t       { return (*C.int64_t)(v.valuePtr()) }
func (v *wafObject) uint64ValuePtr() *C.uint64_t     { return (*C.uint64_t)(v.valuePtr()) }
func (v *wafObject) stringValuePtr() **C.char        { return (**C.char)(v.valuePtr()) }

func (v *wafObject) setUint64(n C.uint64_t) {
	v._type = wafUintType
	*v.uint64ValuePtr() = n
}

func (v *wafObject) setInt64(n C.int64_t) {
	v._type = wafIntType
	*v.int64ValuePtr() = n
}

func (v *wafObject) setString(str *C.char, length C.uint64_t) {
	v._type = wafStringType
	v.nbEntries = C.uint64_t(length)
	*v.stringValuePtr() = str
}

func (v *wafObject) string() *C.char {
	return *v.stringValuePtr()
}

func (v *wafObject) setInvalid() {
	*v = wafObject{}
}

func (v *wafObject) setContainer(typ C.DDWAF_OBJ_TYPE, length C.size_t) error {
	// Allocate the zero'd array.
	var a *C.ddwaf_object
	if length > 0 {
		a = (*C.ddwaf_object)(C.calloc(length, C.sizeof_ddwaf_object))
		if a == nil {
			return ErrOutOfMemory
		}
		incNbLiveCObjects()
		*v.arrayValuePtr() = a
		v.setLength(C.uint64_t(length))
	}
	v._type = typ
	return nil
}

func (v *wafObject) setArrayContainer(length C.size_t) error {
	return v.setContainer(wafArrayType, length)
}

func (v *wafObject) setMapContainer(length C.size_t) error {
	return v.setContainer(wafMapType, length)
}

func (v *wafObject) setMapKey(key *C.char, length C.uint64_t) {
	v.parameterName = key
	v.parameterNameLength = length
}

func (v *wafObject) mapKey() *C.char {
	return v.parameterName
}

func (v *wafObject) setLength(length C.uint64_t) {
	v.nbEntries = length
}

func (v *wafObject) length() C.uint64_t {
	return v.nbEntries
}

func (v *wafObject) index(i C.uint64_t) *wafObject {
	if C.uint64_t(i) >= v.nbEntries {
		panic(errors.New("out of bounds access to waf array"))
	}
	// Go pointer arithmetic equivalent to the C expression `a->value.array[i]`
	base := uintptr(unsafe.Pointer(*v.arrayValuePtr()))
	return (*wafObject)(unsafe.Pointer(base + C.sizeof_ddwaf_object*uintptr(i)))
}

// nbLiveCObjects is a simple monitoring of the number of C allocations.
// Tests can read the value to check the count is back to 0.
var nbLiveCObjects uint64

func incNbLiveCObjects() {
	atomic.AddUint64(&nbLiveCObjects, 1)
}

func decNbLiveCObjects() {
	atomic.AddUint64(&nbLiveCObjects, ^uint64(0))
}

// cstring returns the C string of the given Go string `str` with up to maxWAFStringSize bytes, along with the string
// size that was allocated and copied.
func cstring(str string, maxLength int) (*C.char, int, error) {
	// Limit the maximum string size to copy
	l := len(str)
	if l > maxLength {
		l = maxLength
	}
	// Copy the string up to l.
	// The copy is required as the pointer will be stored into the C structures,
	// so using a Go pointer is impossible.
	cstr := C.CString(str[:l])
	if cstr == nil {
		return nil, 0, errOutOfMemory
	}
	incNbLiveCObjects()
	return cstr, l, nil
}

func free(v *wafObject) {
	if v == nil {
		return
	}
	// Free the map key if any
	if key := v.mapKey(); key != nil {
		C.free(unsafe.Pointer(v.parameterName))
		decNbLiveCObjects()
	}
	// Free allocated values
	switch v._type {
	case wafInvalidType:
		return
	case wafStringType:
		freeString(v)
	case wafMapType, wafArrayType:
		freeContainer(v)
	}
	// Make the value invalid to make it unusable
	v.setInvalid()
}

func freeString(v *wafObject) {
	C.free(unsafe.Pointer(v.string()))
	decNbLiveCObjects()
}

func freeContainer(v *wafObject) {
	length := v.length()
	for i := C.uint64_t(0); i < length; i++ {
		free(v.index(i))
	}
	if a := *v.arrayValuePtr(); a != nil {
		C.free(unsafe.Pointer(a))
		decNbLiveCObjects()
	}
}
