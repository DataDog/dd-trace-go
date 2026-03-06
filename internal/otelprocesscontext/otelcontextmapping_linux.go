// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build linux

package otelprocesscontext

import (
	"fmt"
	"os"
	"structs"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	otelContextSignature = "OTEL_CTX"
)

var (
	otelContextMappingSize = 2 * os.Getpagesize()

	existingMappingBytes []byte
	publisherPID         int
)

type processContextHeader struct {
	_             structs.HostLayout
	Signature     [8]byte
	Version       uint32
	PayloadSize   uint32
	PublishedAtNs uint64
	PayloadAddr   uint64
}

func tryCreateMemfdMapping(size int) ([]byte, error) {
	fd, err := unix.MemfdCreate(otelContextSignature, unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_NOEXEC_SEAL)
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
	if len(data)+headerSize > otelContextMappingSize {
		return fmt.Errorf("data size is too large for the mapping size")
	}

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

	var wg sync.WaitGroup
	wg.Go(func() {
		header := processContextHeader{
			Version:       2,
			PayloadSize:   uint32(len(data)),
			PublishedAtNs: uint64(time.Now().UnixNano()),
			PayloadAddr:   uint64(addr) + uint64(headerSize),
		}
		copy(mappingBytes[headerSize:], data)
		copy(mappingBytes[:headerSize], unsafe.Slice((*byte)(unsafe.Pointer(&header)), headerSize))
	})
	wg.Wait()
	// write the signature last to ensure that once a process validates the signature, it can safely read the whole data
	copy(mappingBytes, otelContextSignature)

	prctlErr := setAnonymousMappingName(mappingBytes, otelContextSignature)

	// If the memfd mapping failed, we should return an error if the prctl call also failed.
	if memfdErr != nil && prctlErr != nil {
		_ = unix.Munmap(mappingBytes)
		return fmt.Errorf("failed both to create memfd mapping and to set vma anon name: %w, %w", memfdErr, prctlErr)
	}

	existingMappingBytes = mappingBytes
	publisherPID = os.Getpid()
	return nil
}

func setAnonymousMappingName(mappingBytes []byte, name string) error {
	// prctl expects a null-terminated string
	nameNullTerminated, _ := unix.ByteSliceFromString(name)
	return unix.Prctl(
		unix.PR_SET_VMA,
		uintptr(unix.PR_SET_VMA_ANON_NAME),
		uintptr(unsafe.Pointer(&mappingBytes[0])),
		uintptr(otelContextMappingSize),
		uintptr(unsafe.Pointer(&nameNullTerminated[0])), // null-terminated string
	)
}

func updateOtelProcessContextMapping(data []byte) error {
	headerSize := int(unsafe.Sizeof(processContextHeader{}))
	if len(data)+headerSize > otelContextMappingSize {
		return fmt.Errorf("data size is too large for the mapping size")
	}

	header := (*processContextHeader)(unsafe.Pointer(&existingMappingBytes[0]))
	oldPublishedAtNs := header.PublishedAtNs
	// Memory barrier to ensure that the header is updated before the data is written
	atomic.StoreUint64(&header.PublishedAtNs, 0)

	copy(existingMappingBytes[headerSize:], data)
	header.PayloadSize = uint32(len(data))
	// Payload address is the same as the previous mapping

	newPublishedAtNs := oldPublishedAtNs
	for newPublishedAtNs == oldPublishedAtNs {
		newPublishedAtNs = uint64(time.Now().UnixNano())
	}
	atomic.StoreUint64(&header.PublishedAtNs, newPublishedAtNs)
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
