// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package profiler

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"testing"
	"testing/synctest"
	"time"

	kgzip "github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestNewCompressionPipeline(t *testing.T) {
	plainData := []byte("hello world")
	gzip1Data := compressData(t, plainData, gzip1Compression)
	gzip6Data := compressData(t, plainData, gzip6Compression)
	zstdData := compressData(t, plainData, zstdCompression)

	tests := []struct {
		in   compression
		out  compression
		data []byte
		want []byte
	}{
		{noCompression, gzip1Compression, plainData, gzip1Data},
		{noCompression, gzip6Compression, plainData, gzip6Data},
		{noCompression, zstdCompression, plainData, zstdData},
		{noCompression, noCompression, plainData, plainData},
		{gzip1Compression, gzip1Compression, gzip1Data, gzip1Data},
		{gzip6Compression, gzip6Compression, gzip6Data, gzip6Data},
		{gzip1Compression, gzip6Compression, gzip1Data, gzip6Data},
		{gzip6Compression, gzip1Compression, gzip6Data, gzip1Data},
		{gzip1Compression, zstdCompression, gzip1Data, zstdData},
		{gzip6Compression, zstdCompression, gzip6Data, zstdData},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%s->%s", test.in, test.out), func(t *testing.T) {
			var pipelineBuilder compressionPipelineBuilder
			pipeline, err := pipelineBuilder.Build(test.in, test.out)
			require.NoError(t, err)
			buf := &bytes.Buffer{}
			pipeline.Reset(buf)
			_, err = pipeline.Write(test.data)
			require.NoError(t, err)
			require.NoError(t, pipeline.Close())
			require.Equal(t, test.want, buf.Bytes())
		})
	}
}

// TestGzipDecodingRecompressorInvalidInput verifies that the recompressor
// surfaces an error (instead of deadlocking) when its input is not a valid
// gzip stream. A naive implementation spawns a goroutine that calls
// kgzip.NewReader, which fails on a bad header. Without explicitly closing
// the read end of the pipe, the goroutine exits while the caller's pw.Write
// blocks forever waiting for a reader that no longer exists.
//
// The test runs inside a testing/synctest bubble so that deadlock detection
// is deterministic instead of relying on a wall-clock timeout: synctest.Wait
// returns once every goroutine in the bubble is durably blocked or has
// exited, and we then assert that Write actually returned.
func TestGzipDecodingRecompressorInvalidInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gzipOut, err := kgzip.NewWriterLevel(nil, gzip6Compression.level)
		require.NoError(t, err)
		r := newGzipDecodingRecompressor(gzipOut)
		r.Reset(io.Discard)
		// Always close so the synctest bubble drains cleanly even
		// when the recompressor is deadlocked: pw.Close unblocks
		// any pending Write, and <-r.err unblocks the goroutine
		// started by Reset.
		defer r.Close()

		// Non-gzip data large enough to overflow whatever buffer
		// kgzip.NewReader uses internally for header parsing. With
		// io.Pipe being unbuffered, any write left over after the
		// recompressor's goroutine errors out will block forever
		// absent a fix.
		data := bytes.Repeat([]byte("not a gzip stream\n"), 4096)

		writeDone := make(chan error, 1)
		go func() {
			_, werr := r.Write(data)
			writeDone <- werr
		}()

		// Block until every goroutine in the bubble is either
		// durably blocked or has exited. With the fix in place,
		// the recompressor's goroutine closes pr with an error,
		// Write returns that error, and writeDone receives a
		// value. Without the fix, Write stays blocked on the
		// pipe and writeDone is empty.
		synctest.Wait()

		select {
		case werr := <-writeDone:
			require.Error(t, werr, "expected an error for non-gzip input")
		default:
			t.Error("deadlock: Write did not return after recompressor goroutine exited")
		}
	})
}

// checkZstdLevel checks that data is zstd-compressed with the given level
func checkZstdLevel(t *testing.T, data []byte, level zstd.EncoderLevel) {
	t.Helper()
	require.NotEmpty(t, data)
	zr, err := zstd.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	in := new(bytes.Buffer)
	_, err = io.Copy(in, zr)
	require.NoError(t, err)
	out := new(bytes.Buffer)
	zw, err := zstd.NewWriter(out, zstd.WithEncoderLevel(level))
	require.NoError(t, err)
	_, err = io.Copy(zw, in)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.Equal(t, data, out.Bytes())
}

func TestDebugCompressionEnv(t *testing.T) {
	mustZstdDecompress := func(t *testing.T, b []byte) {
		t.Helper()
		r, err := zstd.NewReader(bytes.NewReader(b))
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, r)
		require.NoError(t, err)
	}

	t.Run("default", func(t *testing.T) {
		p := startTestProfiler(t, 1, WithDeltaProfiles(false), WithProfileTypes(CPUProfile, HeapProfile, BlockProfile), WithPeriod(time.Millisecond)).ReceiveProfile(t)
		mustZstdDecompress(t, p.attachments["cpu.pprof"])
		mustZstdDecompress(t, p.attachments["heap.pprof"])
		mustZstdDecompress(t, p.attachments["block.pprof"])
	})

	t.Run("explicit-gzip", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "gzip")
		p := startTestProfiler(t, 1, WithProfileTypes(HeapProfile, BlockProfile), WithPeriod(time.Millisecond)).ReceiveProfile(t)
		r, err := gzip.NewReader(bytes.NewReader(p.attachments["delta-heap.pprof"]))
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, r)
		require.NoError(t, err)
	})

	t.Run("explicit-gzip-already-gzipped-input", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "gzip")
		p := startTestProfiler(t, 1, WithProfileTypes(CPUProfile), WithPeriod(time.Millisecond)).ReceiveProfile(t)
		r, err := gzip.NewReader(bytes.NewReader(p.attachments["cpu.pprof"]))
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, r)
		require.NoError(t, err)
	})

	t.Run("zstd-delta", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "zstd-3")
		p := startTestProfiler(t, 1, WithProfileTypes(CPUProfile, HeapProfile), WithPeriod(time.Millisecond)).ReceiveProfile(t)
		checkZstdLevel(t, p.attachments["cpu.pprof"], zstd.SpeedBetterCompression)
		checkZstdLevel(t, p.attachments["delta-heap.pprof"], zstd.SpeedBetterCompression)
	})

	t.Run("zstd-no-delta", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "zstd-3")
		p := startTestProfiler(t, 1, WithDeltaProfiles(false), WithProfileTypes(HeapProfile), WithPeriod(time.Millisecond)).ReceiveProfile(t)
		checkZstdLevel(t, p.attachments["heap.pprof"], zstd.SpeedBetterCompression)
	})

	t.Run("zstd-2", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "zstd-2")
		p := startTestProfiler(t, 1, WithProfileTypes(HeapProfile), WithPeriod(time.Millisecond)).ReceiveProfile(t)
		checkZstdLevel(t, p.attachments["delta-heap.pprof"], zstd.SpeedDefault)
	})

	t.Run("zstd-no-level", func(t *testing.T) {
		t.Setenv("DD_PROFILING_DEBUG_COMPRESSION_SETTINGS", "zstd")
		p := startTestProfiler(t, 1, WithProfileTypes(CPUProfile, HeapProfile), WithPeriod(time.Millisecond)).ReceiveProfile(t)
		checkZstdLevel(t, p.attachments["cpu.pprof"], zstd.SpeedDefault)
		checkZstdLevel(t, p.attachments["delta-heap.pprof"], zstd.SpeedDefault)
	})
}

func compressData(t testing.TB, data []byte, c compression) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	switch c.algorithm {
	case compressionAlgorithmGzip:
		// NB: we need to use the same gzip implementation here as we do
		// in the compressor since "level" isn't actually a stable thing
		// across implementations
		gw, err := kgzip.NewWriterLevel(buf, c.level)
		require.NoError(t, err)
		_, err = gw.Write(data)
		require.NoError(t, err)
		require.NoError(t, gw.Close())
	case compressionAlgorithmZstd:
		level := zstd.SpeedDefault
		if l, ok := zstdLevels[c.level]; ok {
			level = l
		}
		zw, err := zstd.NewWriter(buf, zstd.WithEncoderLevel(level))
		require.NoError(t, err)
		_, err = zw.Write(data)
		require.NoError(t, err)
		require.NoError(t, zw.Close())
	default:
		t.Fatalf("unsupported compression algorithm: %s", c.algorithm)
	}
	return buf.Bytes()
}

func BenchmarkRecompression(b *testing.B) {
	inputdata, err := os.ReadFile("internal/fastdelta/testdata/big-heap.pprof")
	if err != nil {
		b.Fatal(err)
	}
	inputs := []struct {
		inAlg    compression
		outLevel zstd.EncoderLevel
	}{
		{gzip1Compression, zstd.SpeedDefault},
		{gzip1Compression, zstd.SpeedBetterCompression},
		{gzip1Compression, zstd.SpeedBestCompression},
		{gzip6Compression, zstd.SpeedDefault},
		{gzip6Compression, zstd.SpeedBetterCompression},
		{gzip6Compression, zstd.SpeedBestCompression},
	}
	for _, in := range inputs {
		b.Run(fmt.Sprintf("%s-%s", in.inAlg.String(), in.outLevel), func(b *testing.B) {
			data := compressData(b, inputdata, in.inAlg)
			b.ResetTimer()
			var pipelineBuilder compressionPipelineBuilder
			for b.Loop() {
				encoder, err := pipelineBuilder.getZstdEncoder(in.outLevel)
				if err != nil {
					b.Fatal(err)
				}
				z := newGzipDecodingRecompressor(encoder)
				z.Reset(io.Discard)
				if _, err := z.Write(data); err != nil {
					b.Fatal(err)
				}
				if err := z.Close(); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(
				(float64(b.N*len(data))/(1024*1024))/
					(float64(b.Elapsed().Seconds())),
				"MiB/s",
			)
		})
	}

	for _, in := range inputs {
		b.Run(fmt.Sprintf("decomp-recomp-%s-%s", in.inAlg.String(), in.outLevel), func(b *testing.B) {
			// For comparison with the pipe-based recompressor, to
			// see if it's any faster to just decompress and
			// recompress serially
			data := compressData(b, inputdata, in.inAlg)
			b.ResetTimer()
			for b.Loop() {
				buf := new(bytes.Buffer)
				gr, err := kgzip.NewReader(bytes.NewReader(data))
				if err != nil {
					b.Fatal(err)
				}
				io.Copy(buf, gr)
				zw, err := zstd.NewWriter(io.Discard, zstd.WithEncoderLevel(in.outLevel))
				if err != nil {
					b.Fatal(err)
				}
				io.Copy(zw, buf)
				zw.Close()
			}
			b.StopTimer()
			b.ReportMetric(
				(float64(b.N*len(data))/(1024*1024))/
					(float64(b.Elapsed().Seconds())),
				"MiB/s",
			)
		})
	}

	for _, level := range []zstd.EncoderLevel{zstd.SpeedDefault, zstd.SpeedBetterCompression, zstd.SpeedBestCompression} {
		b.Run(fmt.Sprintf("no-compression-zstd-%s", level), func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				zw, err := zstd.NewWriter(io.Discard, zstd.WithEncoderLevel(level))
				if err != nil {
					b.Fatal(err)
				}
				zw.Write(inputdata)
				zw.Close()
			}
			b.StopTimer()
			b.ReportMetric(
				(float64(b.N*len(inputdata))/(1024*1024))/
					(float64(b.Elapsed().Seconds())),
				"MiB/s",
			)
		})
	}
}
