package pipelines

import (
	"encoding/binary"
	"hash/fnv"
	"math/rand"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type Pipeline struct {
	hash     uint64
	callTime time.Time
	service string
	edge    string
}

// Merge merges multiple pipelines
func Merge(pipelines []Pipeline) Pipeline {
	if len(pipelines) == 0 {
		return Pipeline{}
	}
	// for now, randomly select a pipeline.
	n := rand.Intn(len(pipelines))
	return pipelines[n]
}

func nodeHash(service, edge string) uint64 {
	b := make([]byte, 0, len(service) + len(edge))
	b = append(b, service...)
	b = append(b, edge...)
	h := fnv.New64()
	h.Write(b)
	return h.Sum64()
}

func pipelineHash(nodeHash, parentHash uint64) uint64 {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b, nodeHash)
	binary.LittleEndian.PutUint64(b[8:], parentHash)
	h := fnv.New64()
	h.Write(b)
	return h.Sum64()
}

func New() Pipeline {
	now := time.Now()
	p := Pipeline{
		hash:     0,
		callTime: now,
		service:  globalconfig.ServiceName(),
	}
	return p.setCheckpoint("", now)
}

func (p Pipeline) SetCheckpoint(edge string) Pipeline {
	return p.setCheckpoint(edge, time.Now())
}

func (p Pipeline) setCheckpoint(edge string, t time.Time) Pipeline {
	child := Pipeline{
		hash:     pipelineHash(nodeHash(p.service, edge), p.hash),
		callTime: p.callTime,
		service:  p.service,
		edge:     edge,
	}
	if processor := getGlobalProcessor(); processor != nil {
		select {
		case processor.in <- statsPoint{
			service:               p.service,
			receivingPipelineName: edge,
			parentHash:            p.hash,
			hash:                  child.hash,
			timestamp:             t.UnixNano(),
			latency:               t.Sub(p.callTime).Nanoseconds(),
		}:
		default:
			log.Error("Processor input channel full, disregarding stats point.")
		}
	}
	return child
}
