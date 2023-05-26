// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"

	"github.com/DataDog/sketches-go/ddsketch/encoding"
)

const (
	// PropagationKey is the key to use to propagate the pathway between services.
	PropagationKey       = "dd-pathway-ctx"
	PropagationKeyBase64 = "dd-pathway-ctx-base64"
)

// Encode encodes the pathway
func (p Pathway) Encode() []byte {
	data := make([]byte, 8, 20)
	binary.LittleEndian.PutUint64(data, p.hash)
	encoding.EncodeVarint64(&data, p.pathwayStart.UnixNano()/int64(time.Millisecond))
	encoding.EncodeVarint64(&data, p.edgeStart.UnixNano()/int64(time.Millisecond))
	return data
}

// Decode decodes a pathway
func Decode(data []byte) (p Pathway, err error) {
	if len(data) < 8 {
		return p, errors.New("hash smaller than 8 bytes")
	}
	p.hash = binary.LittleEndian.Uint64(data)
	data = data[8:]
	pathwayStart, err := encoding.DecodeVarint64(&data)
	if err != nil {
		return p, err
	}
	edgeStart, err := encoding.DecodeVarint64(&data)
	if err != nil {
		return p, err
	}
	p.pathwayStart = time.Unix(0, pathwayStart*int64(time.Millisecond))
	p.edgeStart = time.Unix(0, edgeStart*int64(time.Millisecond))
	return p, nil
}

// EncodeStr encodes a pathway context into a string using base64 encoding.
func (p Pathway) EncodeStr() string {
	b := p.Encode()
	return base64.StdEncoding.EncodeToString(b)
}

// DecodeStr decodes a pathway context from a string using base64 encoding.
func DecodeStr(str string) (p Pathway, err error) {
	data, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return p, err
	}
	return Decode(data)
}
