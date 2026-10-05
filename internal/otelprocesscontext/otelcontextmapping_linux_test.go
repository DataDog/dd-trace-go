// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

//go:build linux

package otelprocesscontext

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

func getContextFromMapping(fields []string) []byte {
	addrs := strings.SplitN(fields[0], "-", 2)
	if len(addrs) < 2 {
		return nil
	}
	vaddr, err := strconv.ParseUint(addrs[0], 16, 64)
	if err != nil {
		return nil
	}

	header := (*processContextHeader)(unsafe.Pointer(uintptr(vaddr)))
	if string(header.Signature[:]) != otelContextSignature {
		return nil
	}
	if header.Version != 2 {
		return nil
	}
	if header.MonotonicPublishedAtNs == 0 {
		return nil
	}

	payload := make([]byte, header.PayloadSize)
	copy(payload, unsafe.Slice((*byte)(unsafe.Pointer(uintptr(header.PayloadAddr))), header.PayloadSize))
	return payload
}

func isOtelContextName(name string) bool {
	return name == "[anon:OTEL_CTX]" ||
		name == "[anon_shmem:OTEL_CTX]" ||
		strings.HasPrefix(name, "/memfd:OTEL_CTX")
}

func getContextMapping(mapsFile io.Reader) ([]byte, error) {
	content, err := io.ReadAll(mapsFile)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 || !isOtelContextName(fields[5]) {
			continue
		}
		if payload := getContextFromMapping(fields); payload != nil {
			return payload, nil
		}
	}

	return nil, errors.New("no context mapping found")
}

func readProcessLevelContext() ([]byte, error) {
	mapsFile, err := os.Open("/proc/self/maps")
	if err != nil {
		return nil, err
	}
	defer mapsFile.Close()

	return getContextMapping(mapsFile)
}

func restoreOtelProcessContextMapping(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		removeOtelProcessContextMapping()
	})
}

func TestCreateOtelProcessContextMapping(t *testing.T) {
	restoreOtelProcessContextMapping(t)

	payload := []byte("hello world")
	err := CreateOtelProcessContextMapping(payload)
	require.NoError(t, err)

	ctx, err := readProcessLevelContext()
	require.NoError(t, err)
	require.Equal(t, payload, ctx)
}

func TestCreateOtelProcessContextMappingLargePayload(t *testing.T) {
	restoreOtelProcessContextMapping(t)

	headerSize := int(unsafe.Sizeof(processContextHeader{}))
	largePayload := make([]byte, minOtelContextMappingSize-headerSize+1)
	rand.NewChaCha8([32]byte([]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ123456"))).Read(largePayload)

	err := CreateOtelProcessContextMapping(largePayload)
	require.NoError(t, err)

	ctx, err := readProcessLevelContext()
	require.NoError(t, err)
	require.Equal(t, largePayload, ctx)
}

func TestUpdateOtelProcessContextMapping(t *testing.T) {
	restoreOtelProcessContextMapping(t)

	err := CreateOtelProcessContextMapping([]byte("initial payload"))
	require.NoError(t, err)

	updatedPayload := []byte("updated payload")
	err = CreateOtelProcessContextMapping(updatedPayload)
	require.NoError(t, err)

	ctx, err := readProcessLevelContext()
	require.NoError(t, err)
	require.Equal(t, updatedPayload, ctx)
}

func TestUpdateOtelProcessContextMappingShorterPayload(t *testing.T) {
	restoreOtelProcessContextMapping(t)

	err := CreateOtelProcessContextMapping([]byte("longer initial payload"))
	require.NoError(t, err)

	shortPayload := []byte("short")
	err = CreateOtelProcessContextMapping(shortPayload)
	require.NoError(t, err)

	ctx, err := readProcessLevelContext()
	require.NoError(t, err)
	require.Equal(t, shortPayload, ctx)
}

func TestUpdateOtelProcessContextMappingRejectsOversizedPayload(t *testing.T) {
	restoreOtelProcessContextMapping(t)

	err := CreateOtelProcessContextMapping([]byte("initial"))
	require.NoError(t, err)

	headerSize := int(unsafe.Sizeof(processContextHeader{}))
	oversizedPayload := make([]byte, minOtelContextMappingSize-headerSize+1)

	err = CreateOtelProcessContextMapping(oversizedPayload)
	require.ErrorIs(t, err, ErrPayloadTooLarge)
}

func TestUpdateOtelProcessContextMappingChangesTimestamp(t *testing.T) {
	restoreOtelProcessContextMapping(t)

	err := CreateOtelProcessContextMapping([]byte("initial"))
	require.NoError(t, err)

	header := (*processContextHeader)(unsafe.Pointer(&existingMappingBytes[0]))
	initialTimestamp := atomic.LoadUint64(&header.MonotonicPublishedAtNs)
	require.NotZero(t, initialTimestamp)

	err = CreateOtelProcessContextMapping([]byte("updated"))
	require.NoError(t, err)

	newTimestamp := atomic.LoadUint64(&header.MonotonicPublishedAtNs)
	require.NotZero(t, newTimestamp)
	require.NotEqual(t, newTimestamp, initialTimestamp)
}

// restoreMemfd returns a cleanup function that restores tryCreateMemfdMapping.
func mockMemfdWithFailure(t *testing.T) {
	t.Helper()
	orig := tryCreateMemfdMapping
	t.Cleanup(func() { tryCreateMemfdMapping = orig })
	tryCreateMemfdMapping = func(_ int) ([]byte, error) {
		return nil, errors.New("memfd failed")
	}
}

// restorePrctl returns a cleanup function that restores setAnonymousMappingName.
func mockPrctlWithFailure(t *testing.T) {
	t.Helper()
	orig := setAnonymousMappingName
	t.Cleanup(func() { setAnonymousMappingName = orig })
	setAnonymousMappingName = func(_ []byte, _ string) error {
		return errors.New("prctl failed")
	}
}

// TestCreateOtelProcessContextMappingMemfdFails verifies that the mapping is
// still created successfully when memfd_create is unavailable, falling back to
// an anonymous mmap named via prctl.
func TestCreateOtelProcessContextMappingMemfdFails(t *testing.T) {
	restoreOtelProcessContextMapping(t)
	mockMemfdWithFailure(t)

	payload := []byte("hello from anon mmap")
	require.NoError(t, CreateOtelProcessContextMapping(payload))

	ctx, err := readProcessLevelContext()
	require.NoError(t, err)
	require.Equal(t, payload, ctx)
}

// TestCreateOtelProcessContextMappingPrctlFails verifies that a prctl failure
// is not fatal when memfd_create succeeded (memfd is sufficient for discoverability).
func TestCreateOtelProcessContextMappingPrctlFails(t *testing.T) {
	restoreOtelProcessContextMapping(t)
	mockPrctlWithFailure(t)

	payload := []byte("hello from memfd")
	require.NoError(t, CreateOtelProcessContextMapping(payload))
	require.NotNil(t, existingMappingBytes)
}

// TestCreateOtelProcessContextMappingBothFail verifies that an error is
// returned and no mapping is left behind when both memfd_create and prctl fail.
func TestCreateOtelProcessContextMappingBothFail(t *testing.T) {
	restoreOtelProcessContextMapping(t)
	mockMemfdWithFailure(t)
	mockPrctlWithFailure(t)

	err := CreateOtelProcessContextMapping([]byte("hello"))
	require.Error(t, err)
	require.Nil(t, existingMappingBytes) // mapping must be cleaned up on error
}

func TestPublishOtelProcessContext(t *testing.T) {
	restoreOtelProcessContextMapping(t)

	pc := &ProcessContext{
		Resource: &resourcev1.Resource{
			Attributes: []*commonv1.KeyValue{
				{Key: "deployment.environment.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "production"}}},
				{Key: "host.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "my-host"}}},
				{Key: "service.instance.id", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "abc-123"}}},
				{Key: "service.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "my-service"}}},
				{Key: "service.version", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "1.2.3"}}},
				{Key: "telemetry.sdk.language", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "go"}}},
				{Key: "telemetry.sdk.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "dd-trace-go"}}},
				{Key: "telemetry.sdk.version", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "1.0.0"}}},
				{Key: "container.id", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "1234567890"}}},
			},
		},
		ExtraAttributes: []*commonv1.KeyValue{
			{Key: "datadog.process_tags", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "tag1=value1,tag2=value2"}}},
		},
	}
	require.NoError(t, PublishProcessContext(pc))

	ctx, err := readProcessLevelContext()
	require.NoError(t, err)
	var pc2 = &ProcessContext{}
	require.NoError(t, proto.Unmarshal(ctx, pc2))

	require.EqualExportedValues(t, pc, pc2)
}
