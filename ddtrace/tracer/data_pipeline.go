package tracer

import (
	"encoding/binary"
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/mapping"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/spaolacci/murmur3"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"time"
	// "github.com/spaolacci/murmur3"
)

// for now, use just a basic sketch
var sketchMapping, _ = mapping.NewLogarithmicMapping(0.01)

type dataPipeline struct {
	latencies []ddtrace.PipelineLatency
	callTime time.Time
	service string
	env string
	pipelineName string
}

func (p *dataPipeline) GetCallTime() time.Time {
	return p.callTime
}

func (p *dataPipeline) GetLatencies() []ddtrace.PipelineLatency {
	return p.latencies
}

func newSummary() *ddsketch.DDSketch {
	// todo[piochelepiotr] Use paginated buffered store.
	return ddsketch.NewDDSketch(sketchMapping, store.NewCollapsingLowestDenseStore(1000), store.NewCollapsingLowestDenseStore(1000))
}

func newDataPipeline(service string) *dataPipeline {
	summary := newSummary()
	summary.Add(0)
	now := time.Now()
	p := &dataPipeline{
		latencies: []ddtrace.PipelineLatency{
			{
				Hash: 0,
				Summary: summary,
			},
		},
		callTime: now,
		service: service,
	}
	return p.setCheckpoint("", now)
}

func nodeHash(service, receivingPipelineName string) uint64 {
	b := make([]byte, 0, len(service) + len(receivingPipelineName))
	b = append(b, service...)
	b = append(b, receivingPipelineName...)
	// todo[piochelepiotr] Using external library for that critical part is certainly not ideal.
	return murmur3.Sum64(b)
}

func pipelineHash(nodeHash, parentHash uint64) uint64 {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b, nodeHash)
	binary.LittleEndian.PutUint64(b[8:], parentHash)
	return murmur3.Sum64(b)
}

func (p *dataPipeline) SetCheckpoint(receivingPipelineName string) ddtrace.DataPipeline {
	return p.setCheckpoint(receivingPipelineName, time.Now())
}

func (p *dataPipeline) setCheckpoint(receivingPipelineName string, t time.Time) *dataPipeline {
	latency := float64(t.Sub(p.callTime)) / float64(time.Second)
	if latency < 0 {
		latency = 0
	}
	hash := nodeHash(p.service, receivingPipelineName)
	totalLatencies := make([]ddtrace.PipelineLatency, 0, len(p.latencies))
	for _, l := range p.latencies {
		var summary *ddsketch.DDSketch
		if latency == 0 {
			summary = l.Summary
		} else {
			summary = newSummary()
			l.Summary.ForEach(func(value, count float64) bool {
				summary.Add(value + latency)
				return false
			})
		}
		totalLatencies = append(totalLatencies, ddtrace.PipelineLatency{
			Summary: summary,
			Hash: pipelineHash(hash, l.Hash),
		})
	}
	if tracer, ok := internal.GetGlobalTracer().(*tracer); ok {
		for i, latency := range totalLatencies {
			log.Info("send point to stats aggregator")
			select {
			case tracer.pipelineStats.In <- pipelineStatsPoint{
				service: p.service,
				receivingPipelineName: p.pipelineName,
				parentHash: p.latencies[i].Hash,
				pipelineHash: latency.Hash,
				timestamp: t.UnixNano(),
				summary: latency.Summary,
			}:
			default:
				log.Error("Pipeline stats channel full, disregarding stats point.")
			}
		}
	}
	d := dataPipeline{
		latencies: totalLatencies,
		callTime: t,
		service: p.service,
	}
	return &d
}