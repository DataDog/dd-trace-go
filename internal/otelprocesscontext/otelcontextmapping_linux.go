// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build linux

package otelprocesscontext

import (
	"errors"
	"fmt"
	"os"
	"structs"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ErrPayloadTooLarge is returned when the payload exceeds the fixed mapping size
var ErrPayloadTooLarge = errors.New("data size is too large for the mapping size")

const (
	otelContextSignature = "OTEL_CTX"
)

var (
	minOtelContextMappingSize = 2 * os.Getpagesize()

	existingMappingBytes []byte
	publisherPID         int
)

type processContextHeader struct {
	_                      structs.HostLayout
	Signature              [8]byte
	Version                uint32
	PayloadSize            uint32
	MonotonicPublishedAtNs uint64
	PayloadAddr            uint64
}

var tryCreateMemfdMapping = func(size int) ([]byte, error) {
	fallbackFlags := unix.MFD_CLOEXEC | unix.MFD_ALLOW_SEALING
	fd, err := unix.MemfdCreate(otelContextSignature, fallbackFlags|unix.MFD_NOEXEC_SEAL)
	if err != nil && err == unix.EINVAL {
		// Older kernels may not support MFD_NOEXEC_SEAL, so we try again without it.
		fd, err = unix.MemfdCreate(otelContextSignature, fallbackFlags)
	}
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		return nil, fmt.Errorf("failed to ftruncate: %w", err)
	}
	return unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE)
}

func CreateOtelProcessContextMapping(data []byte) error {
	if existingMappingBytes == nil || publisherPID != os.Getpid() {
		return createOtelProcessContextMapping(data)
	}
	return updateOtelProcessContextMapping(data)
}

func createOtelProcessContextMapping(data []byte) error {
	headerSize := int(unsafe.Sizeof(processContextHeader{}))
	otelContextMappingSize := roundUpToPageSize(max(minOtelContextMappingSize, len(data)+headerSize))

	// Try memfd_create first; fall back to anonymous mapping
	mappingBytes, memfdErr := tryCreateMemfdMapping(otelContextMappingSize)
	if memfdErr != nil {
		var err error
		mappingBytes, err = unix.Mmap(
			-1,                     // fd = -1 for an anonymous mapping
			0,                      // offset
			otelContextMappingSize, // length
			unix.PROT_READ|unix.PROT_WRITE,
			unix.MAP_PRIVATE|unix.MAP_ANONYMOUS,
		)
		if err != nil {
			return fmt.Errorf("failed to mmap: %w", err)
		}
	}

	if err := unix.Madvise(mappingBytes, unix.MADV_DONTFORK); err != nil {
		_ = unix.Munmap(mappingBytes)
		return fmt.Errorf("failed to madvise: %w", err)
	}
	addr := uintptr(unsafe.Pointer(&mappingBytes[0]))

	header := (*processContextHeader)(unsafe.Pointer(&mappingBytes[0]))
	copy(header.Signature[:], otelContextSignature)
	header.Version = 2
	header.PayloadSize = uint32(len(data))
	header.PayloadAddr = uint64(addr) + uint64(headerSize)
	copy(mappingBytes[headerSize:], data)

	monotonicPublishedAtNs, err := getMonotonicClockTime()
	if err != nil {
		_ = unix.Munmap(mappingBytes)
		return fmt.Errorf("failed to get monotonic clock time: %w", err)
	}

	// Write MonotonicPublishedAtNs atomically last to signal that the mapping is ready to be read.
	atomic.StoreUint64(&header.MonotonicPublishedAtNs, monotonicPublishedAtNs)

	prctlErr := setAnonymousMappingName(mappingBytes, otelContextSignature)

	// Either memfd or prctl need to succeed for the mapping to be findable by other processes.
	// If both failed, return an error.
	if memfdErr != nil && prctlErr != nil {
		_ = unix.Munmap(mappingBytes)
		return fmt.Errorf("failed both to create memfd mapping and to set vma anon name: %w, %w", memfdErr, prctlErr)
	}

	existingMappingBytes = mappingBytes
	publisherPID = os.Getpid()
	return nil
}

var setAnonymousMappingName = func(mappingBytes []byte, name string) error {
	// prctl expects a null-terminated string
	nameNullTerminated, _ := unix.ByteSliceFromString(name)
	return unix.Prctl(
		unix.PR_SET_VMA,
		uintptr(unix.PR_SET_VMA_ANON_NAME),
		uintptr(unsafe.Pointer(&mappingBytes[0])),
		uintptr(len(mappingBytes)),
		uintptr(unsafe.Pointer(&nameNullTerminated[0])), // null-terminated string
	)
}

func updateOtelProcessContextMapping(data []byte) error {
	headerSize := int(unsafe.Sizeof(processContextHeader{}))
	if len(data)+headerSize > len(existingMappingBytes) {
		return ErrPayloadTooLarge
	}

	header := (*processContextHeader)(unsafe.Pointer(&existingMappingBytes[0]))
	oldPublishedAtNs := header.MonotonicPublishedAtNs
	// Set MonotonicPublishedAtNs to 0 to signal that the mapping is being updated and is no longer valid.
	atomic.StoreUint64(&header.MonotonicPublishedAtNs, 0)
	// Memory barrier to ensure that following writes are not reordered before the MonotonicPublishedAtNs write.
	memoryBarrier()

	copy(existingMappingBytes[headerSize:], data)
	header.PayloadSize = uint32(len(data))
	// Payload address is the same as the previous mapping

	newPublishedAtNs, err := getMonotonicClockTime()
	if err != nil {
		return fmt.Errorf("failed to get monotonic clock time: %w", err)
	}

	// Ensure that the new published at time is different from the old one
	if newPublishedAtNs == oldPublishedAtNs {
		newPublishedAtNs = oldPublishedAtNs + 1
	}

	// Write MonotonicPublishedAtNs atomically last to signal that the mapping is ready to be read.
	atomic.StoreUint64(&header.MonotonicPublishedAtNs, newPublishedAtNs)
	_ = setAnonymousMappingName(existingMappingBytes, otelContextSignature)
	return nil
}

func removeOtelProcessContextMapping() error {
	//Check publisher PID to check that the process has not forked.
	//It should not be necessary for Go, but just in case.
	if existingMappingBytes == nil || publisherPID != os.Getpid() {
		return nil
	}

	err := unix.Munmap(existingMappingBytes)
	if err != nil {
		return fmt.Errorf("failed to munmap: %w", err)
	}
	existingMappingBytes = nil
	publisherPID = 0
	return nil
}

func roundUpToPageSize(size int) int {
	pageSize := os.Getpagesize()
	return (size + pageSize - 1) & ^(pageSize - 1)
}

func getMonotonicClockTime() (uint64, error) {
	var now unix.Timespec
	err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &now)
	if err != nil {
		return 0, fmt.Errorf("failed to get clock time: %w", err)
	}
	return uint64(now.Nano()), nil
}

func memoryBarrier() {
	// On ARM64, atomic add will compile as LDADDAL which will act as a full memory barrier.
	var fence uint64
	atomic.AddUint64(&fence, 0)
}
