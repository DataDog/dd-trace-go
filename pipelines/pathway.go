// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package pipelines

import (
	"encoding/binary"
	"hash/fnv"
	"log"
	"math/rand"
	"time"
)

// Pathway is used to monitor how payloads are sent across different services.
// An example Pathway would be:
// service A -- edge 1 --> service B -- edge 2 --> service C
// So it's a branch of services (we also call them "nodes") connected via edges.
// As the payload is sent around, we save the start time (start of service A),
// and the start time of the previous service.
// This allows us to measure the latency of each edge, as well as the latency from origin of any service.
type Pathway struct {
	// hash is the hash of the current node, of the parent node, and of the edge that connects the parent node
	// to this node.
	hash uint64
	// pathwayStart is the start of the first node in the Pathway
	pathwayStart time.Time
	// edgeStart is the start of the previous node.
	edgeStart time.Time
	// service is the service of the current node.
	service string
	// edge is the name of the edge connecting this node, and its parent.
	edge string
}

// Merge merges multiple pathways into one.
// The current implementation samples one resulting Pathway. A future implementation could be more clever
// and actually merge the Pathways.
func Merge(pathways []Pathway) Pathway {
	if len(pathways) == 0 {
		return Pathway{}
	}
	// Randomly select a pathway to propagate downstream.
	n := rand.Intn(len(pathways))
	return pathways[n]
}

func nodeHash(service, edge string) uint64 {
	b := make([]byte, 0, len(service)+len(edge))
	b = append(b, service...)
	b = append(b, edge...)
	h := fnv.New64()
	h.Write(b)
	return h.Sum64()
}

func pathwayHash(nodeHash, parentHash uint64) uint64 {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b, nodeHash)
	binary.LittleEndian.PutUint64(b[8:], parentHash)
	h := fnv.New64()
	h.Write(b)
	return h.Sum64()
}

// NewPathway creates a new pathway.
func NewPathway() Pathway {
	return newPathway(time.Now())
}

func newPathway(now time.Time) Pathway {
	p := Pathway{
		hash:         0,
		pathwayStart: now,
		edgeStart:    now,
		service:      getService(),
	}
	return p.setCheckpoint("", now)
}

// SetCheckpoint sets a checkpoint on a pathway.
func (p Pathway) SetCheckpoint(edge string) Pathway {
	return p.setCheckpoint(edge, time.Now())
}

func (p Pathway) setCheckpoint(edge string, now time.Time) Pathway {
	child := Pathway{
		hash:         pathwayHash(nodeHash(p.service, edge), p.hash),
		pathwayStart: p.pathwayStart,
		edgeStart:    now,
		service:      p.service,
		edge:         edge,
	}
	if processor := getGlobalProcessor(); processor != nil {
		select {
		case processor.in <- statsPoint{
			service:        p.service,
			edge:           edge,
			parentHash:     p.hash,
			hash:           child.hash,
			timestamp:      now.UnixNano(),
			pathwayLatency: now.Sub(p.pathwayStart).Nanoseconds(),
			edgeLatency:    now.Sub(p.edgeStart).Nanoseconds(),
		}:
		default:
			log.Println("WARN: Processor input channel full, disregarding stats point.")
		}
	}
	return child
}
