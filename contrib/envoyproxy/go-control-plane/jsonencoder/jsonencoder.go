package jsonencoder

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/DataDog/go-libddwaf/v4"
	"github.com/DataDog/go-libddwaf/v4/waferrors"

	jsoniter "github.com/json-iterator/go"
)

type JsonEncoder struct {
	truncations        map[libddwaf.TruncationReason][]int
	config             libddwaf.EncoderConfig
	initiallyTruncated bool
	iter               *jsoniter.Iterator
	iterCfg            jsoniter.API
}

// NewJSONEncoder creates a new JSON encoder with the given data and max size.
// It truncates the data if it exceeds the max size and sets the truncated flag.
func NewJSONEncoder(dataToEncode []byte, maxSize int) *JsonEncoder {
	truncated := false
	if len(dataToEncode) > maxSize {
		dataToEncode = dataToEncode[:maxSize]
		truncated = true
	}

	cfg := jsoniter.ConfigCompatibleWithStandardLibrary
	iter := cfg.BorrowIterator(dataToEncode)

	return &JsonEncoder{
		truncations:        make(map[libddwaf.TruncationReason][]int),
		initiallyTruncated: truncated,
		iter:               iter,
		iterCfg:            cfg,
	}
}

// NewJSONEncoderTruncated creates a new JSON encoder with the given data.
// It sets the truncated flag to true.
func NewJSONEncoderTruncated(dataToEncode []byte) *JsonEncoder {
	cfg := jsoniter.ConfigCompatibleWithStandardLibrary
	iter := cfg.BorrowIterator(dataToEncode)

	return &JsonEncoder{
		truncations:        make(map[libddwaf.TruncationReason][]int),
		initiallyTruncated: true,
		iter:               iter,
		iterCfg:            cfg,
	}
}

// Encode encodes the provided json data from the JSON Encoder into a WAFObject.
// It implements the `libddwaf.Encodable` interface.
func (e *JsonEncoder) Encode(config libddwaf.EncoderConfig, obj *libddwaf.WAFObject, _ int) (map[libddwaf.TruncationReason][]int, error) {
	defer e.iterCfg.ReturnIterator(e.iter)
	e.config = config
	e.truncations = make(map[libddwaf.TruncationReason][]int)

	err := e.encode(obj, e.config.MaxObjectDepth)
	if err != nil && (errors.Is(err, waferrors.ErrTimeout) || !e.initiallyTruncated) {
		// Return an error if a waf timeout error occurred, or we are in normal parsing mode
		return nil, err
	}

	if obj.IsUnusable() {
		// Do not return an invalid root object
		return nil, fmt.Errorf("invalid json at root")
	}

	head, tail := getIteratorHeadAndTail(e.iter)
	if head < tail {
		// If the iterator head is less than the tail, it means that there are still bytes left in the buffer,
		// thus alerting that a structural parsing error occurred (other than due to truncation)
		return nil, fmt.Errorf("malformed JSON: %w", err)
	}

	return e.truncations, nil

}

// Truncations returns all truncations that happened since the last call to `truncations()`, and clears the internal
// list. This is a map from truncation reason to the list of un-truncated value sizes.
func (e *JsonEncoder) Truncations() map[libddwaf.TruncationReason][]int {
	result := e.truncations
	e.truncations = nil
	return result
}

// addTruncation records a truncation event.
func (e *JsonEncoder) addTruncation(reason libddwaf.TruncationReason, size int) {
	if e.truncations == nil {
		e.truncations = make(map[libddwaf.TruncationReason][]int, 3)
	}
	e.truncations[reason] = append(e.truncations[reason], size)
}

func (e *JsonEncoder) encode(obj *libddwaf.WAFObject, depth int) error {
	if e.config.Timer.Exhausted() {
		return waferrors.ErrTimeout
	}

	var err error

	switch e.iter.WhatIsNext() {
	case jsoniter.ObjectValue:
		err = e.encodeObject(obj, depth-1)
	case jsoniter.ArrayValue:
		err = e.encodeArray(obj, depth-1)
	case jsoniter.StringValue:
		s := e.iter.ReadString()
		if err = e.iter.Error; err == nil || err == io.EOF {
			e.encodeString(s, obj)
		}
	case jsoniter.NumberValue:
		jsonNbr := e.iter.ReadNumber()
		if err = e.iter.Error; err == nil || err == io.EOF {
			err = nil
			e.encodeJSONNumber(jsonNbr, obj)
		}
	case jsoniter.BoolValue:
		b := e.iter.ReadBool()
		if err = e.iter.Error; err == nil || err == io.EOF {
			err = nil
			obj.SetBool(b)
		}
	case jsoniter.NilValue:
		e.iter.ReadNil()
		if err = e.iter.Error; err == nil || err == io.EOF {
			err = nil
			obj.SetNil()
		}
	default:
		err = fmt.Errorf("unexpected JSON token: %v", e.iter.WhatIsNext())
	}

	return err
}

func (e *JsonEncoder) encodeJSONNumber(num json.Number, obj *libddwaf.WAFObject) {
	// Important to attempt int64 first, as this is lossless. Values that are either too small or too
	// large to be represented as int64 can be represented as float64, but this can be lossy.
	if i, err := num.Int64(); err == nil {
		obj.SetInt(i)
		return
	}

	if f, err := num.Float64(); err == nil {
		obj.SetFloat(f)
		return
	}

	// Could not store as int64 nor float, so we'll store it as a string...
	e.encodeString(num.String(), obj)
}

func (e *JsonEncoder) encodeString(str string, obj *libddwaf.WAFObject) {
	strLen := len(str)
	if strLen > e.config.MaxStringSize {
		str = str[:e.config.MaxStringSize]
		e.addTruncation(libddwaf.StringTooLong, strLen)
	}

	obj.SetString(e.config.Pinner, str)
}

// encodeMapKeyFromString takes a string and a wafObject and sets the map key attribute on the wafObject to the supplied
// string. The key may be truncated if it exceeds the maximum string size allowed by the jsonEncoder.
func (e *JsonEncoder) encodeMapKeyFromString(keyStr string, obj *libddwaf.WAFObject) {
	size := len(keyStr)
	if size > e.config.MaxStringSize {
		keyStr = keyStr[:e.config.MaxStringSize]
		e.addTruncation(libddwaf.StringTooLong, size)
	}

	obj.SetMapKey(e.config.Pinner, keyStr)
}

func (e *JsonEncoder) encodeObject(parentObj *libddwaf.WAFObject, depth int) error {
	if e.config.Timer.Exhausted() {
		return waferrors.ErrTimeout
	}

	if depth < 0 {
		e.addTruncation(libddwaf.ObjectTooDeep, e.config.MaxObjectDepth-depth)
		e.iter.Skip()
		if e.iter.Error != nil {
			return e.iter.Error
		}

		return nil
	}

	length, err := getContainerLength(e, true)
	if err != nil && (!errors.Is(err, io.EOF) || (errors.Is(err, waferrors.ErrTimeout) || !e.initiallyTruncated)) {
		// Return error only when timeout and for normal parsing mode (not truncated)
		return err
	}

	objMap := parentObj.SetMap(e.config.Pinner, uint64(length))
	if length == 0 {
		// If there is an error, early return it as the json is malformed and nothing left to parse
		if err != nil {
			return err
		}

		e.iter.Skip()
		if e.iter.Error != nil {
			return e.iter.Error
		}

		return nil
	}

	count := 0
	var errRec error

	e.iter.ReadObjectCB(func(i *jsoniter.Iterator, field string) bool {
		if e.config.Timer.Exhausted() {
			errRec = waferrors.ErrTimeout
			return false
		}

		if count >= length {
			e.iter.Skip()
			return true
		}

		if e.iter.Error != nil {
			// Note: We reject every object where the key field could not be parsed.
			// A valid key field is considered to be a string wrapped inside quotes followed by a colon.
			// We don't do partial parsing of the key, like assuming the key was full even if we don't detect the closing quote,
			// this could cause bad API Security schema generation.
			errRec = e.iter.Error
			return false
		}

		// The key of the object is set even if the value is invalid
		entryObj := &objMap[count]
		e.encodeMapKeyFromString(field, entryObj)
		count++

		errEncodeValue := e.encode(entryObj, depth)
		if errEncodeValue != nil {
			errRec = errEncodeValue
			return false
		}

		return true
	})

	parentObj.NbEntries = uint64(count)

	return errRec
}

func (e *JsonEncoder) encodeArray(parentObj *libddwaf.WAFObject, depth int) error {
	if e.config.Timer.Exhausted() {
		return waferrors.ErrTimeout
	}

	if depth < 0 {
		e.addTruncation(libddwaf.ObjectTooDeep, e.config.MaxObjectDepth-depth)
		e.iter.Skip()
		if e.iter.Error != nil {
			return e.iter.Error
		}
		return nil
	}

	length, err := getContainerLength(e, false)
	if err != nil && (!errors.Is(err, io.EOF) || (errors.Is(err, waferrors.ErrTimeout) || !e.initiallyTruncated)) {
		// Return error only when timeout and for normal parsing mode (not truncated)
		return err
	}

	objArray := parentObj.SetArray(e.config.Pinner, uint64(length))
	if length == 0 {
		e.iter.Skip()
		if e.iter.Error != nil {
			return e.iter.Error
		}
		return nil
	}

	count := 0
	var errRec error

	e.iter.ReadArrayCB(func(_ *jsoniter.Iterator) bool {
		if e.config.Timer.Exhausted() {
			errRec = waferrors.ErrTimeout
			return false
		}

		// We want to skip all the elements in the array if the length is reached
		if count >= length {
			e.iter.Skip()
			return true
		}

		objElem := &objArray[count]
		errEncodeValue := e.encode(objElem, depth)
		if errEncodeValue != nil {
			errRec = errEncodeValue
			return false
		}

		if !objElem.IsUnusable() {
			count++
		}

		return true
	})

	parentObj.NbEntries = uint64(count)

	if errRec != nil {
		return errRec
	}
	return e.iter.Error
}
