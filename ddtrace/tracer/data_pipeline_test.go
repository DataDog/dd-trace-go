package tracer

import (
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"testing"
	"time"
)

func TestSerializeDataPipeline(t *testing.T) {
	s1, _ := ddsketch.NewDefaultDDSketch(0.01)
	s1.Add(4)
	s2, _ := ddsketch.NewDefaultDDSketch(0.01)
	s2.Add(8)
	now := time.Now()
	pipeline := dataPipeline{
		callTime: now,
		latencies: []ddtrace.PipelineLatency{
			{
				Hash: 1,
				Summary: s1,
			},
			{
				Hash: 2,
				Summary: s2,
			},
		},
	}
	data, err := pipeline.ToBaggage()
	assert.Nil(t, err)
	tracer := tracer{config: &config{serviceName: "service"}}
	convertedPipeline, err := tracer.DataPipelineFromBaggage(data)
	assert.Nil(t, err)
	assert.Equal(t, pipeline.callTime.UnixNano(), convertedPipeline.GetCallTime().UnixNano())
	convertedLatencies := convertedPipeline.GetLatencies()
	assert.Equal(t, len(pipeline.latencies), len(convertedLatencies))
	for i, l := range pipeline.latencies {
		assert.Equal(t, l.Hash, convertedLatencies[i].Hash)
		assert.InEpsilonf(t, l.Summary.GetSum(), convertedLatencies[i].Summary.GetSum(), 0.0001, "")
	}
}
