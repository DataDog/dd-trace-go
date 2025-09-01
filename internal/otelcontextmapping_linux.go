// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build linux

package internal

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"golang.org/x/sys/unix"
)

const (
	// These two constants are not in x/sys/unix by default; copy them from <linux/prctl.h>.
	//nolint:revive
	PR_SET_VMA = 0x53564D41
	//nolint:revive
	PR_SET_VMA_ANON_NAME = 0

	otelContextSignature = "OTEL_CTX"
)

var (
	otelContextMappingSize = 2 * os.Getpagesize()

	existingMappingBytes []byte
)

type processContextHeader struct {
	Signature   [8]byte
	Version     uint32
	PayloadSize uint32
	PayloadAddr uintptr
}

func CreateOtelProcessContextMapping(data []byte) error {
	if existingMappingBytes != nil {
		// Unmap the previous mapping if it exists
		err := unix.Munmap(existingMappingBytes)
		if err != nil {
			return fmt.Errorf("failed to munmap previous mapping: %w", err)
		}
	}

	headerSize := int(unsafe.Sizeof(processContextHeader{}))
	if len(data)+headerSize > otelContextMappingSize {
		return fmt.Errorf("data size is too large for the mapping size")
	}

	mappingBytes, err := unix.Mmap(
		-1,                     // fd = -1 for an anonymous mapping
		0,                      // offset
		otelContextMappingSize, // length
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_ANONYMOUS,
	)
	if err != nil {
		return fmt.Errorf("failed to mmap: %w", err)
	}

	err = unix.Madvise(mappingBytes, unix.MADV_DONTFORK)
	if err != nil {
		_ = unix.Munmap(mappingBytes)
		return fmt.Errorf("failed to madvise: %w", err)
	}

	addr := uintptr(unsafe.Pointer(&mappingBytes[0]))
	header := processContextHeader{
		Version:     1,
		PayloadSize: uint32(len(data)),
		PayloadAddr: addr + uintptr(headerSize),
	}

	copy(mappingBytes[headerSize:], data)
	copy(mappingBytes[:headerSize], unsafe.Slice((*byte)(unsafe.Pointer(&header)), headerSize))
	// write the signature last to ensure that once a process validates the signature, it can safely read the whole data
	copy(mappingBytes, otelContextSignature)

	err = unix.Mprotect(mappingBytes, unix.PROT_READ)
	if err != nil {
		_ = unix.Munmap(mappingBytes)
		return fmt.Errorf("failed to mprotect: %w", err)
	}

	// prctl expects a null-terminated string
	contextNameNullTerminated, _ := unix.ByteSliceFromString(otelContextSignature)
	err = unix.Prctl(
		PR_SET_VMA,
		uintptr(PR_SET_VMA_ANON_NAME),
		addr,
		uintptr(otelContextMappingSize),
		uintptr(unsafe.Pointer(&contextNameNullTerminated[0])),
	)
	if err != nil {
		// Failure to set the vma anon name is not a critical error (only supported on Linux 5.17+), so we log it at the warning level.
		log.Warn("failed to set vma anon name: %s", err.Error())
	}

	existingMappingBytes = mappingBytes
	return nil
}
