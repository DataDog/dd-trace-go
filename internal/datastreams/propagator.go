// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"
	"unsafe"

	"github.com/DataDog/sketches-go/ddsketch/encoding"
)

type contextKey struct{}

var activePathwayKey = contextKey{}

const (
	// PropagationKeyBase64 is the key to use to propagate the pathway between services.
	PropagationKeyBase64 = "dd-pathway-ctx-base64"
)

// appendEncoded appends the binary encoding of p to dst.
// Maximum encoded length is 8 (hash) + 10 (pathwayStart varint) + 10 (edgeStart varint) = 28 bytes.
// binary.AppendVarint uses the same zig-zag varint encoding as ddsketch/encoding.EncodeVarint64
// without taking a *[]byte pointer, allowing callers' stack buffers to stay on the stack.
func (p Pathway) appendEncoded(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, p.hash)
	dst = binary.AppendVarint(dst, p.pathwayStart.UnixNano()/int64(time.Millisecond))
	dst = binary.AppendVarint(dst, p.edgeStart.UnixNano()/int64(time.Millisecond))
	return dst
}

// Encode encodes the pathway
func (p Pathway) Encode() []byte {
	return p.appendEncoded(make([]byte, 0, 28))
}

// Decode decodes a pathway
func Decode(ctx context.Context, data []byte) (p Pathway, outCtx context.Context, err error) {
	if len(data) < 8 {
		return p, ctx, errors.New("hash smaller than 8 bytes")
	}
	p.hash = binary.LittleEndian.Uint64(data)
	data = data[8:]
	pathwayStart, err := encoding.DecodeVarint64(&data)
	if err != nil {
		return p, ctx, err
	}
	edgeStart, err := encoding.DecodeVarint64(&data)
	if err != nil {
		return p, ctx, err
	}
	p.pathwayStart = time.Unix(0, pathwayStart*int64(time.Millisecond))
	p.edgeStart = time.Unix(0, edgeStart*int64(time.Millisecond))
	return p, ContextWithPathway(ctx, p), nil
}

// EncodeBase64 encodes a pathway context into a string using base64 encoding.
func (p Pathway) EncodeBase64() string {
	var rawBuf [28]byte
	raw := p.appendEncoded(rawBuf[:0])
	var b64Buf [40]byte
	n := base64.StdEncoding.EncodedLen(len(raw))
	base64.StdEncoding.Encode(b64Buf[:n], raw)
	return string(b64Buf[:n])
}

// DecodeBase64 decodes a pathway context from a string using base64 encoding.
func DecodeBase64(ctx context.Context, str string) (p Pathway, outCtx context.Context, err error) {
	if len(str) <= 64 {
		var rawBuf [48]byte
		src := unsafe.Slice(unsafe.StringData(str), len(str))
		n, decErr := base64.StdEncoding.Decode(rawBuf[:], src)
		if decErr != nil {
			return p, ctx, decErr
		}
		return Decode(ctx, rawBuf[:n])
	}
	data, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return p, ctx, err
	}
	return Decode(ctx, data)
}

type pathwayContext struct {
	context.Context
	pathway Pathway
}

func (c *pathwayContext) Value(key any) any {
	if key == activePathwayKey {
		return c.pathway
	}
	return c.Context.Value(key)
}

// ContextWithPathway returns a copy of the given context which includes the pathway p.
func ContextWithPathway(ctx context.Context, p Pathway) context.Context {
	if ctx == nil {
		panic("cannot create context from nil parent")
	}
	return &pathwayContext{Context: ctx, pathway: p}
}

// PathwayFromContext returns the pathway contained in the given context, and whether a
// pathway is found in ctx.
func PathwayFromContext(ctx context.Context) (p Pathway, ok bool) {
	if ctx == nil {
		return p, false
	}
	if pc, ok := ctx.(*pathwayContext); ok {
		return pc.pathway, true
	}
	v := ctx.Value(activePathwayKey)
	if p, ok := v.(Pathway); ok {
		return p, true
	}
	return p, false
}
