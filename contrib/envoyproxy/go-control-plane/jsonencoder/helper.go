package jsonencoder

import (
	"github.com/DataDog/go-libddwaf/v4"
	"github.com/DataDog/go-libddwaf/v4/waferrors"
	jsoniter "github.com/json-iterator/go"
	"reflect"
	"unsafe"
)

// getContainerLength get the length of a JSON container (object or array)
func getContainerLength(e *JsonEncoder, isObject bool) (int, error) {
	var errRec error
	count := 0
	startHead, startDepth := getIteratorHeadAndDepth(e.iter)

	elemCB := func() bool {
		if e.config.Timer.Exhausted() {
			errRec = waferrors.ErrTimeout
			return false
		}

		if e.iter.Error != nil {
			return false
		}

		count++ // not sure if need to ++ before or after the skip

		e.iter.Skip()
		if e.iter.Error != nil {
			return false
		}

		return true
	}

	if isObject {
		e.iter.ReadObjectCB(func(_ *jsoniter.Iterator, _ string) bool {
			return elemCB()
		})
	} else {
		e.iter.ReadArrayCB(func(_ *jsoniter.Iterator) bool {
			return elemCB()
		})
	}

	if count > e.config.MaxContainerSize {
		e.addTruncation(libddwaf.ContainerTooLarge, count)
		count = e.config.MaxContainerSize
	}

	if e.iter.Error != nil {
		errRec = e.iter.Error
	}

	// Reset the iterator as before the skip of the container
	setIteratorHeadAndDepth(e.iter, startHead, startDepth)
	e.iter.Error = nil

	return count, errRec
}

func getIteratorHeadAndDepth(iter *jsoniter.Iterator) (head, depth int) {
	elem := reflect.ValueOf(iter).Elem()
	head = int(elem.FieldByIndex([]int{3}).Int())
	depth = int(elem.FieldByIndex([]int{5}).Int())
	return
}

func getIteratorHeadAndTail(iter *jsoniter.Iterator) (head, tail int) {
	elem := reflect.ValueOf(iter).Elem()
	head = int(elem.FieldByIndex([]int{3}).Int())
	tail = int(elem.FieldByIndex([]int{4}).Int())
	return
}

func setIteratorHeadAndDepth(iter *jsoniter.Iterator, head, depth int) {
	elem := reflect.ValueOf(iter).Elem()

	fieldHead := elem.FieldByIndex([]int{3})
	reflect.NewAt(fieldHead.Type(), unsafe.Pointer(fieldHead.UnsafeAddr())).Elem().Set(reflect.ValueOf(head))

	fieldTail := elem.FieldByIndex([]int{5})
	reflect.NewAt(fieldTail.Type(), unsafe.Pointer(fieldTail.UnsafeAddr())).Elem().Set(reflect.ValueOf(depth))
}
