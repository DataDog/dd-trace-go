package jsonencoder

import (
	"errors"
	"io"
	"reflect"
	"unsafe"

	"github.com/DataDog/go-libddwaf/v4"
	"github.com/DataDog/go-libddwaf/v4/waferrors"

	jsoniter "github.com/json-iterator/go"
)

// getContainerLength get the length of a JSON container (object or array)
// and returns the number of elements in it (truncated if it exceeds the max container size).
func getContainerLength(e *JsonEncoder, isObject bool) (int, error) {
	var errRec error
	count := 0
	startHead, tail, startDepth := getIteratorHeadTailAndDepth(e.iter)

	elemCB := func() bool {
		if e.config.Timer.Exhausted() {
			errRec = waferrors.ErrTimeout
			return false
		}

		count++

		e.iter.Skip()
		return true
	}

	if isObject {
		e.iter.ReadObjectCB(func(_ *jsoniter.Iterator, k string) bool {
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

	// Return immediately if the timer is exhausted or if an error has been recorded during the iteration
	if errRec != nil && errors.Is(errRec, waferrors.ErrTimeout) {
		return 0, errRec
	}

	// If an error is detected here in the iterator, it might be because of a structural invalid json
	// We can't know really know if the error is due to an EOF or not because the iterator would have seeked
	// to the end of the buffer and overwritten the EOF error by another parsing error.
	// Here we decided to detect and catch the EOF manually and bubble it up,
	// thus keeping a partial parsing result when in that configuration

	if e.iter.Error != nil {
		head := getIteratorHead(e.iter)
		if head == tail {
			errRec = io.EOF
		} else {
			errRec = e.iter.Error
		}
	}

	// Reset the iterator as before the skip of the container
	setIteratorHeadAndDepth(e.iter, startHead, startDepth)
	e.iter.Error = nil

	return count, errRec
}

// getIteratorHeadTailAndDepth returns the head, tail and depth of the jsoniter iterator
func getIteratorHeadTailAndDepth(iter *jsoniter.Iterator) (head, tail, depth int) {
	elem := reflect.ValueOf(iter).Elem()
	head = int(elem.FieldByIndex([]int{3}).Int())
	tail = int(elem.FieldByIndex([]int{4}).Int())
	depth = int(elem.FieldByIndex([]int{5}).Int())
	return
}

// getIteratorHeadAndTail returns the head and tail of the jsoniter iterator
func getIteratorHeadAndTail(iter *jsoniter.Iterator) (head, tail int) {
	elem := reflect.ValueOf(iter).Elem()
	head = int(elem.FieldByIndex([]int{3}).Int())
	tail = int(elem.FieldByIndex([]int{4}).Int())
	return
}

func getIteratorHead(iter *jsoniter.Iterator) (head int) {
	elem := reflect.ValueOf(iter).Elem()
	head = int(elem.FieldByIndex([]int{3}).Int())
	return
}

// setIteratorHeadAndDepth sets the head and depth of the jsoniter iterator
func setIteratorHeadAndDepth(iter *jsoniter.Iterator, head, depth int) {
	elem := reflect.ValueOf(iter).Elem()

	fieldHead := elem.FieldByIndex([]int{3})
	reflect.NewAt(fieldHead.Type(), unsafe.Pointer(fieldHead.UnsafeAddr())).Elem().Set(reflect.ValueOf(head))

	fieldTail := elem.FieldByIndex([]int{5})
	reflect.NewAt(fieldTail.Type(), unsafe.Pointer(fieldTail.UnsafeAddr())).Elem().Set(reflect.ValueOf(depth))
}
