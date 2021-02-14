package erpc

import (
	"encoding/binary"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/modern-go/reflect2"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

const (
	rpcCmd = 0xdeadc001

	// ERPCMaxDataSize maximum size of data of a request
	ERPCMaxDataSize = 256
)

// GetHostByteOrder guesses the hosts byte order
func GetHostByteOrder() binary.ByteOrder {
	var i int32 = 0x01020304
	u := unsafe.Pointer(&i)
	pb := (*byte)(u)
	b := *pb
	if b == 0x04 {
		return binary.LittleEndian
	}

	return binary.BigEndian
}

// ByteOrder holds the hosts byte order
var ByteOrder binary.ByteOrder

func init() {
	ByteOrder = GetHostByteOrder()
}

// spanIdentifiers is used to uniquely identify a span
type spanIdentifiers struct {
	SpanID  uint64
	TraceID uint64
}

// ERPC defines a krpc object
type ERPC struct {
	mode ERPCMode

	// goroutine tracker mode attributes
	fd         int
	goIDOffset uint64

	// memory segment mode attributes
	sharedSegment        []byte
	sharedIndexModulo    uint64
	goroutineSegment     []byte
	goroutineIndexModulo int64
}

// ERPCRequest defines a EPRC request
type ERPCRequest struct {
	OP   uint8
	Data [ERPCMaxDataSize]byte
}

// Request generates an ioctl syscall with the required request
func (k *ERPC) Request(req *ERPCRequest) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(k.fd), rpcCmd, uintptr(unsafe.Pointer(req))); errno != 0 {
		if errno != syscall.ENOTTY {
			return errno
		}
	}

	return nil
}

// ERPCMode defines how the eRPC client should be configured
type ERPCMode uint8

const (
	Deactivated ERPCMode = iota
	GoroutineTracker
	MemorySegment
)

// NewERPCClient returns a new eRPC client
func NewERPCClient(mode ERPCMode) (*ERPC, error) {
	var err error
	client := ERPC{
		mode: mode,
	}

	switch mode {
	case GoroutineTracker:

		// compute goID offset
		gType := reflect2.TypeByName("runtime.g").(reflect2.StructType)
		if gType == nil {
			panic("failed to get runtime.g type")
		}
		goidField := gType.FieldByName("goid")
		client.goIDOffset = uint64(goidField.Offset())

		// duplicate stdout to provide a valid file descriptor
		client.fd, err = syscall.Dup(syscall.Stdout)
		if err != nil {
			return nil, errors.Wrap(err, "couldn't duplicate stdout")
		}

		// send goroutine tracker request
		if err := client.SendGoroutineTrackerRequest(); err != nil {
			return nil, errors.Wrap(err, "failed to send goroutine tracker eRPC request")
		}

	case MemorySegment:
		segmentSize := 1000 * os.Getpagesize()

		// Allocate a shared memory segment to store the tid <-> span mapping
		// This first memory segment will be shared by the kernel.
		// The segment size determines the amount of concurrent threads that can be tracked.
		// Ex: 4Mb (segment size) / 16 b (size of span ID + trace ID) = 256,000 threads
		client.sharedSegment, err = unix.Mmap(0, 0, segmentSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_ANON)
		if err != nil {
			return nil, errors.Wrap(err, "failed to mmap memory segment")
		}

		// Allocate a second memory segment to store the goid <-> span mapping
		// This second memory segment will not be shared by the kernel. It will only be used as a cache to track
		// what span is currently tracked by each goroutine.
		// The segment size determines the amount of concurrent goroutines that be tracked.
		// Ex: 4Mb (segment size) / 16 b (size of span ID + trace ID) = 256,000 goroutines
		client.goroutineSegment, err = unix.Mmap(0, 0, segmentSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_ANON)
		if err != nil {
			return nil, errors.Wrap(err, "failed to mmap memory segment")
		}

		// compute the maximum amount of indexed spans that can fit in the allocated memory segments
		client.sharedIndexModulo = uint64(segmentSize / 16)
		client.goroutineIndexModulo = int64(segmentSize / 16)

		// send memory segment
		if err := client.SendMemorySegmentRequest(); err != nil {
			return nil, errors.Wrap(err, "failed to send memory segment over eRPC request")
		}
	case Deactivated:
		return nil, nil
	}

	return &client, nil
}

// SendGoroutineTrackerRequest sends an eRPC request to start a goroutine tracker
func (e *ERPC) SendGoroutineTrackerRequest() error {
	// Send goroutine tracker request
	req := ERPCRequest{
		OP: 4,
	}
	return e.Request(&req)
}

// SendMemorySegmentRequest sends an eRPC request to declare the user space memory segment in which spans are saved
func (e *ERPC) SendMemorySegmentRequest() error {
	// Send memory segment request
	req := ERPCRequest{
		OP: 5,
	}
	ByteOrder.PutUint64(req.Data[0:8], uint64(uintptr(unsafe.Pointer(&e.sharedSegment[0]))))
	ByteOrder.PutUint64(req.Data[8:16], e.sharedIndexModulo)
	return e.Request(&req)
}

// HandleSpanCreationEvent handles a span creation depending on the mode of the eRPC client
func (e *ERPC) HandleSpanCreationEvent(goID int64, spanID, traceID uint64) error {
	switch e.mode {
	case GoroutineTracker:
		return e.SendNewSpan(goID, spanID, traceID)
	case MemorySegment:
		return e.UpdateGoroutineSpan(goID, spanID, traceID)
	case Deactivated:
		return nil
	default:
		return errors.Errorf("unknown erpc mode: %d", e.mode)
	}
}

// HandleRuntimeExecuteEvent handles the scheduling of a goroutine on a thread
func (e *ERPC) HandleRuntimeExecuteEvent(goID int64, threadID uint64) {
	if e.mode != MemorySegment {
		return
	}

	// update the threadID <-> span mapping in the shared memory segment
	offset := (threadID % e.sharedIndexModulo) * 16
	goroutineOffset := (goID % e.goroutineIndexModulo) * 16

	atomic.SwapUint64((*uint64)(unsafe.Pointer(&e.sharedSegment[offset])), *(*uint64)(unsafe.Pointer(&e.goroutineSegment[goroutineOffset])))
	atomic.SwapUint64((*uint64)(unsafe.Pointer(&e.sharedSegment[offset+8])), *(*uint64)(unsafe.Pointer(&e.goroutineSegment[goroutineOffset+8])))
}

// SendNewSpan sends an eRPC request to declare a new span
func (e *ERPC) SendNewSpan(goID int64, spanID, traceID uint64) error {
	// Send span ID request
	req := ERPCRequest{
		OP: 3,
	}

	// no need for a secret token in go, the legitimacy of the request will be confirmed by the call path
	ByteOrder.PutUint64(req.Data[0:8], uint64(0))
	ByteOrder.PutUint64(req.Data[8:16], spanID)
	ByteOrder.PutUint64(req.Data[16:24], traceID)
	ByteOrder.PutUint64(req.Data[24:32], uint64(goID))
	req.Data[32] = byte(1) // golang type
	ByteOrder.PutUint64(req.Data[33:41], e.goIDOffset)

	if err := e.Request(&req); err != nil {
		return err
	}

	// allow the goroutine to be rescheduled, this will ensure it is properly tracked
	runtime.Gosched()
	return nil
}

// UpdateGoroutineSpan Updates the internal goroutine <-> span map
func (e *ERPC) UpdateGoroutineSpan(goID int64, spanID, traceID uint64) error {
	goroutineOffset := (goID % e.goroutineIndexModulo) * 16
	atomic.SwapUint64((*uint64)(unsafe.Pointer(&e.goroutineSegment[goroutineOffset])), spanID)
	atomic.SwapUint64((*uint64)(unsafe.Pointer(&e.goroutineSegment[goroutineOffset+8])), traceID)
	// make sure the current thread is associated with the current span
	e.HandleRuntimeExecuteEvent(goID, Mid())
	return nil
}
