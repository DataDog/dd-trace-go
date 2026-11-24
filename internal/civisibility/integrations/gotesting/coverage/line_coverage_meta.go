// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"errors"
	"math"
	"strconv"
)

const (
	// lineCoverageMetaVersion is the version of the block table encoding.
	lineCoverageMetaVersion = 1
	// lineCoverageBlockFlagInit marks the blocks of the body of a `func init()`.
	lineCoverageBlockFlagInit = 1
)

var errInvalidLineCoverageMeta = errors.New("invalid line coverage block table")

// lineCoverageBlock is one block of an instrumented package. A block with no
// lines has startLine > endLine.
type lineCoverageBlock struct {
	file      int32 // index of the collector's file
	startLine int32
	endLine   int32
	isInit    bool
}

// decodeLineCoverageMeta decodes the block table that orchestrion embeds in
// each instrumented package:
//
//	uvarint version
//	uvarint file count, then for each file: uvarint length, bytes
//	uvarint block count, then for each block:
//	  uvarint file index, uvarint start line, uvarint end line - start line, byte flags
//
// fileIndex maps a file name of the table to an index of the collector's files.
func decodeLineCoverageMeta(meta string, numBlocks int, fileIndex func(name string) int32) ([]lineCoverageBlock, error) {
	r := metaReader{data: meta}
	if version := r.uvarint(); r.err == nil && version != lineCoverageMetaVersion {
		return nil, errors.New("unsupported line coverage block table version " + strconv.FormatUint(version, 10))
	}
	fileCount := r.uvarint()
	if r.err != nil || fileCount > uint64(len(meta)) {
		return nil, errInvalidLineCoverageMeta
	}
	files := make([]int32, fileCount)
	for i := range files {
		name := r.string(r.uvarint())
		if r.err != nil {
			return nil, r.err
		}
		files[i] = fileIndex(name)
	}
	if blockCount := r.uvarint(); r.err != nil || blockCount != uint64(numBlocks) {
		return nil, errInvalidLineCoverageMeta
	}
	blocks := make([]lineCoverageBlock, numBlocks)
	for i := range blocks {
		file, start, span, flags := r.uvarint(), r.uvarint(), r.uvarint(), r.byte()
		if r.err != nil || file >= fileCount || start > math.MaxInt32 || span > math.MaxInt32-start {
			return nil, errInvalidLineCoverageMeta
		}
		b := lineCoverageBlock{
			file:      files[file],
			startLine: int32(start),
			endLine:   int32(start + span),
			isInit:    flags&lineCoverageBlockFlagInit != 0,
		}
		if b.startLine == 0 {
			b.startLine, b.endLine = 1, 0
		}
		blocks[i] = b
	}
	if r.pos != len(r.data) {
		return nil, errInvalidLineCoverageMeta
	}
	return blocks, nil
}

// metaReader reads a block table. Its first error sticks.
type metaReader struct {
	data string
	pos  int
	err  error
}

func (r *metaReader) uvarint() uint64 {
	var x uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b := r.byte()
		if r.err != nil {
			return 0
		}
		if b < 0x80 {
			if shift == 63 && b > 1 {
				break
			}
			return x | uint64(b)<<shift
		}
		x |= uint64(b&0x7f) << shift
	}
	r.err = errInvalidLineCoverageMeta
	return 0
}

func (r *metaReader) byte() byte {
	if r.err != nil {
		return 0
	}
	if r.pos >= len(r.data) {
		r.err = errInvalidLineCoverageMeta
		return 0
	}
	b := r.data[r.pos]
	r.pos++
	return b
}

func (r *metaReader) string(n uint64) string {
	if r.err != nil {
		return ""
	}
	if n > uint64(len(r.data)-r.pos) {
		r.err = errInvalidLineCoverageMeta
		return ""
	}
	s := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return s
}
