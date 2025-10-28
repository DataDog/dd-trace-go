// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package profiler

/*
In order to save bandwidth and networking cost, the profiler compresses the
profiling data it collects before sending it to Datadog. This file contains
the logic that controls this compression.

For historical reasons, the profiler has used varying levels of compression for
different profile types. pprof files coming from the Go runtime are already
compressed using gzip-1 (aka gzip.BestSpeed) [1], so they were uploaded as-is.
Other profiles that were produced (or derived) by the profiler itself have been
either compressed with gzip-6 (aka gzip.DefaultCompression) or were left
uncompressed. The exact details are captured in the legacyOutputCompression
function below.

This legacy compression strategy was haphazard and not designed to achieve an
optimal tradeoff between overhead and cost savings. Due to this, it is going to
be succeeded by a new compression strategy. The implementation for it will
follow in the next update to this file.

[1] https://github.com/golang/go/blob/go1.24.3/src/runtime/pprof/proto.go#L260
*/

import (
	"cmp"
	"fmt"
	"io"
	"strconv"
	"strings"

	kgzip "github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// legacyCompressionStrategy returns the input and output compression to be used
// by the compressor for the given profile type and isDelta flavor using the
// legacy compression strategy.
func legacyCompressionStrategy(pt ProfileType, isDelta bool) (compression, compression) {
	return inputCompression(pt, isDelta), legacyOutputCompression(pt, isDelta)
}

func compressionStrategy(pt ProfileType, isDelta bool, config string) (compression, compression) {
	if config == "" || config == "legacy" {
		return legacyCompressionStrategy(pt, isDelta)
	}
	algorithm, levelStr, _ := strings.Cut(config, "-")
	// Don't bother checking the error. We'll get zero which represents the
	// default, and we we assume this is only going to get used internally
	level, _ := strconv.Atoi(levelStr)
	return inputCompression(pt, isDelta), compression{algorithm: compressionAlgorithm(algorithm), level: level}
}

// inputCompression maps the given profile type and isDelta flavor to the
// compression level that was already applied to the data by the the Go runtime.
// Profiles produced (or derived) by our profiler itself are expected to be
// uncompressed.
func inputCompression(pt ProfileType, isDelta bool) compression {
	switch pt {
	case CPUProfile, GoroutineProfile:
		return gzip1Compression
	case HeapProfile, BlockProfile, MutexProfile:
		if isDelta {
			return noCompression
		}
		return gzip1Compression
	case MetricsProfile, executionTrace, expGoroutineWaitProfile:
		return noCompression
	default:
		panic(fmt.Sprintf("unknown profile type: %s", pt))
	}
}

// legacyOutputCompression maps the given profile type and isDelta flavor to
// a compression level using our legacy compression strategy.
func legacyOutputCompression(pt ProfileType, isDelta bool) compression {
	switch pt {
	case CPUProfile, GoroutineProfile:
		return gzip1Compression
	case expGoroutineWaitProfile:
		return gzip6Compression
	case HeapProfile, BlockProfile, MutexProfile:
		if isDelta {
			return gzip6Compression
		}
		return gzip1Compression
	case executionTrace, MetricsProfile:
		return noCompression
	default:
		panic(fmt.Sprintf("unknown profile type: %s", pt))
	}
}

type compressionAlgorithm string

const (
	compressionAlgorithmNone compressionAlgorithm = "none"
	compressionAlgorithmGzip compressionAlgorithm = "gzip"
	compressionAlgorithmZstd compressionAlgorithm = "zstd"
)

type compression struct {
	algorithm compressionAlgorithm
	level     int
}

func (c compression) String() string {
	if c.algorithm == compressionAlgorithmNone {
		return string(c.algorithm)
	}
	return fmt.Sprintf("%s-%d", c.algorithm, c.level)
}

// Common compression algorithm and level combinations.
var (
	noCompression    = compression{algorithm: compressionAlgorithmNone}
	gzip1Compression = compression{algorithm: compressionAlgorithmGzip, level: 1}
	gzip6Compression = compression{algorithm: compressionAlgorithmGzip, level: 6}
	zstdCompression  = compression{algorithm: compressionAlgorithmZstd, level: 2}
)

var zstdLevels = map[int]zstd.EncoderLevel{
	1: zstd.SpeedFastest,
	2: zstd.SpeedDefault,
	3: zstd.SpeedBetterCompression,
	4: zstd.SpeedBestCompression,
}

func getZstdLevelOrDefault(level int) zstd.EncoderLevel {
	if l, ok := zstdLevels[level]; ok {
		return l
	}
	return zstd.SpeedDefault
}

type compressionPipelineBuilder struct {
	zstdEncoders map[zstd.EncoderLevel]*sharedZstdEncoder
}

func (b *compressionPipelineBuilder) getZstdEncoder(level zstd.EncoderLevel) (*sharedZstdEncoder, error) {
	if b.zstdEncoders == nil {
		b.zstdEncoders = make(map[zstd.EncoderLevel]*sharedZstdEncoder)
	}
	encoder, ok := b.zstdEncoders[level]
	if !ok {
		var err error
		encoder, err = newSharedZstdEncoder(level)
		if err != nil {
			return nil, err
		}
		b.zstdEncoders[level] = encoder
	}
	return encoder, nil
}

// Build returns a compressor that converts the data written to it from the
// expected input compression to the given output compression.
func (b *compressionPipelineBuilder) Build(in compression, out compression) (compressor, error) {
	if in == out {
		return newPassthroughCompressor(), nil
	}

	if in == noCompression && out.algorithm == compressionAlgorithmGzip {
		return kgzip.NewWriterLevel(nil, out.level)
	}

	if in == noCompression && out.algorithm == compressionAlgorithmZstd {
		return b.getZstdEncoder(getZstdLevelOrDefault(out.level))
	}

	if in.algorithm == compressionAlgorithmGzip && out.algorithm == compressionAlgorithmZstd {
		encoder, err := b.getZstdEncoder(getZstdLevelOrDefault(out.level))
		if err != nil {
			return nil, err
		}
		return newZstdRecompressor(encoder), nil
	}

	return nil, fmt.Errorf("unsupported recompression: %s -> %s", in, out)
}

// compressor provides an interface for compressing profiling data. If the input
// is already compressed, it can also act as a re-compressor that decompresses
// the data from one format and then re-compresses it into another format.
type compressor interface {
	io.Writer
	// Reset reset the compressor to the given writer. It may also acquire a
	// shared underlaying resource, so callers must always call Close().
	Reset(w io.Writer)
	// Close closes the compressor and releases any shared underlaying resource.
	Close() error
}

// newPassthroughCompressor returns a compressor that simply passes all data
// through without applying any compression.
func newPassthroughCompressor() *passthroughCompressor {
	return &passthroughCompressor{}
}

type passthroughCompressor struct {
	io.Writer
}

func (r *passthroughCompressor) Reset(w io.Writer) {
	r.Writer = w
}

func (r *passthroughCompressor) Close() error {
	return nil
}

func newZstdRecompressor(encoder *sharedZstdEncoder) *zstdRecompressor {
	return &zstdRecompressor{zstdOut: encoder, err: make(chan error)}
}

type zstdRecompressor struct {
	// err synchronizes finishing writes after closing pw and reports any
	// error during recompression
	err     chan error
	pw      io.WriteCloser
	zstdOut *sharedZstdEncoder
}

func (r *zstdRecompressor) Reset(w io.Writer) {
	r.zstdOut.Reset(w)
	pr, pw := io.Pipe()
	go func() {
		gzr, err := kgzip.NewReader(pr)
		if err != nil {
			r.err <- err
			return
		}
		_, err = io.Copy(r.zstdOut, gzr)
		r.err <- err
	}()
	r.pw = pw
}

func (r *zstdRecompressor) Write(p []byte) (int, error) {
	return r.pw.Write(p)
}

func (r *zstdRecompressor) Close() error {
	r.pw.Close()
	err := <-r.err
	return cmp.Or(err, r.zstdOut.Close())
}

// newSharedZstdEncoder creates a new shared Zstd encoder with the given level.
// It expects the Reset and Close method to be used in an acquire and release
// fashion.
func newSharedZstdEncoder(level zstd.EncoderLevel) (*sharedZstdEncoder, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(level))
	if err != nil {
		return nil, err
	}
	return &sharedZstdEncoder{encoder: encoder, sema: make(chan struct{}, 1)}, nil
}

type sharedZstdEncoder struct {
	encoder *zstd.Encoder
	sema    chan struct{}
}

// Reset acquires the semaphore and resets the encoder to the given writer.
func (s *sharedZstdEncoder) Reset(w io.Writer) {
	s.sema <- struct{}{}
	s.encoder.Reset(w)
}

func (s *sharedZstdEncoder) Write(p []byte) (int, error) {
	return s.encoder.Write(p)
}

// Close releases the semaphore and closes the encoder.
func (s *sharedZstdEncoder) Close() error {
	err := s.encoder.Close()
	<-s.sema
	return err
}
