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
	"errors"
	"math"
	"reflect"
	"unicode"
)

// #include <stdint.h>
import "C"

var (
	errMaxDepth         = errors.New("max depth reached")
	errUnsupportedValue = errors.New("unsupported Go value")
	errOutOfMemory      = errors.New("out of memory")
)

// isIgnoredValueError returns true if the error is only about ignored Go values (errUnsupportedValue or errMaxDepth).
func isIgnoredValueError(err error) bool {
	return err == errUnsupportedValue || err == errMaxDepth
}

// encoder is allows to encode a Go value to a WAF object
type encoder struct {
	// Maximum depth a WAF object can have. Every Go value further this depth is ignored and not encoded into a WAF object.
	maxDepth int
	// Maximum string length. A string longer than this length is truncated to this length.
	maxStringLength int
	// Maximum string length. Everything further this length is ignored.
	maxArrayLength int
	// Maximum map length. Everything further this length is ignored. Given the fact Go maps are unordered, it means
	// WAF map objects created from Go maps larger than this length will have random keys.
	maxMapLength int
}

func (e *encoder) encode(v interface{}) (*wafObject, error) {
	wo := &wafObject{}
	err := e.encodeValue(reflect.ValueOf(v), wo, e.maxDepth)
	if err != nil {
		free(wo)
		return nil, err
	}
	return wo, nil
}

func (e *encoder) encodeValue(v reflect.Value, wo *wafObject, depth int) error {
	switch kind := v.Kind(); kind {
	default:
		return errUnsupportedValue

	case reflect.Bool:
		var b string
		if v.Bool() {
			b = "true"
		} else {
			b = "false"
		}
		return e.encodeString(b, wo)

	case reflect.Ptr, reflect.Interface:
		// The traversal of pointer and interfaces is not accounted in the depth as it has no impact on the WAF object
		// depth
		return e.encodeValue(v.Elem(), wo, depth)

	case reflect.String:
		return e.encodeString(v.String(), wo)

	case reflect.Struct:
		if depth < 0 {
			return errMaxDepth
		}
		return e.encodeStruct(v, wo, depth-1)

	case reflect.Map:
		if depth < 0 {
			return errMaxDepth
		}
		return e.encodeMap(v, wo, depth-1)

	case reflect.Array, reflect.Slice:
		if depth < 0 {
			return errMaxDepth
		}
		return e.encodeArray(v, wo, depth-1)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return e.encodeInt64(v.Int(), wo)

	case reflect.Float32, reflect.Float64:
		return e.encodeInt64(int64(math.Round(v.Float())), wo)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return e.encodeUint64(v.Uint(), wo)
	}
}

func (e *encoder) encodeStruct(v reflect.Value, wo *wafObject, depth int) error {
	// Consider the number of struct fields as the WAF map capacity as some struct fields might not be supported and
	// ignored.
	typ := v.Type()
	nbFields := typ.NumField()
	capacity := nbFields
	if capacity > e.maxMapLength {
		capacity = e.maxMapLength
	}
	if err := wo.setMapContainer(C.uint64_t(capacity)); err != nil {
		return err
	}
	// Encode struct fields
	length := 0
	for i := 0; length < capacity && i < nbFields; i++ {
		field := typ.Field(i)
		// Skip private fields
		fieldName := field.Name
		if len(fieldName) < 1 || unicode.IsLower(rune(fieldName[0])) {
			continue
		}

		mapEntry := wo.index(C.uint64_t(length))
		if err := e.encodeMapKey(reflect.ValueOf(fieldName), mapEntry); isIgnoredValueError(err) {
			continue
		}

		if err := e.encodeValue(v.Field(i), mapEntry, depth); err != nil {
			// Free the map entry in order to free the previously allocated map key
			free(mapEntry)
			if isIgnoredValueError(err) {
				continue
			}
			return err
		}
		length++
	}
	// Update the map length to the actual one
	if length != capacity {
		wo.setLength(C.uint64_t(length))
	}
	return nil
}

func (e *encoder) encodeMap(v reflect.Value, wo *wafObject, depth int) error {
	// Consider the Go map value length the WAF map capacity as some map entries might not be supported and ignored.
	// In this case, the actual map length will be lesser than the Go map value length.
	capacity := v.Len()
	if capacity > e.maxMapLength {
		capacity = e.maxMapLength
	}
	if err := wo.setMapContainer(C.uint64_t(capacity)); err != nil {
		return err
	}
	// Encode map entries
	length := 0
	for iter := v.MapRange(); iter.Next(); {
		if length == capacity {
			break
		}
		mapEntry := wo.index(C.uint64_t(length))
		if err := e.encodeMapKey(iter.Key(), mapEntry); isIgnoredValueError(err) {
			continue
		}
		if err := e.encodeValue(iter.Value(), mapEntry, depth); err != nil {
			// Free the previously allocated map key
			free(mapEntry)
			if isIgnoredValueError(err) {
				continue
			}
			return err
		}
		length++
	}
	// Update the map length to the actual one
	if length != capacity {
		wo.setLength(C.uint64_t(length))
	}
	return nil
}

func (e *encoder) encodeMapKey(v reflect.Value, wo *wafObject) error {
	for {
		switch v.Kind() {
		default:
			return errUnsupportedValue

		case reflect.Ptr, reflect.Interface:
			if v.IsNil() {
				return errUnsupportedValue
			}
			v = v.Elem()

		case reflect.String:
			ckey, length, err := cstring(v.String(), e.maxStringLength)
			if err != nil {
				return err
			}
			wo.setMapKey(ckey, C.uint64_t(length))
			return nil
		}
	}
}

func (e *encoder) encodeArray(v reflect.Value, wo *wafObject, depth int) error {
	// Consider the array length as a capacity as some array values might not be supported and ignored. In this case,
	// the actual length will be lesser than the Go value length.
	length := v.Len()
	capacity := length
	if capacity > e.maxArrayLength {
		capacity = e.maxArrayLength
	}
	if err := wo.setArrayContainer(C.uint64_t(capacity)); err != nil {
		return err
	}
	// Walk the array until we successfully added up to "cap" elements or the Go array length was reached
	currIndex := 0
	for i := 0; currIndex < capacity && i < length; i++ {
		if err := e.encodeValue(v.Index(i), wo.index(C.uint64_t(currIndex)), depth); err != nil {
			if isIgnoredValueError(err) {
				continue
			}
			return err
		}
		// The value has been successfully encoded and added to the array
		currIndex++
	}
	// Update the array length to its actual value in case some array values where ignored
	if currIndex != capacity {
		wo.setLength(C.uint64_t(currIndex))
	}
	return nil
}

func (e *encoder) encodeString(str string, wo *wafObject) error {
	cstr, length, err := cstring(str, e.maxStringLength)
	if err != nil {
		return err
	}
	wo.setString(cstr, C.uint64_t(length))
	return nil
}

func (e *encoder) encodeInt64(n int64, wo *wafObject) error {
	wo.setInt64(C.int64_t(n))
	return nil
}

func (e *encoder) encodeUint64(n uint64, wo *wafObject) error {
	wo.setUint64(C.uint64_t(n))
	return nil
}
