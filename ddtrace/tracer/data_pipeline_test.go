package tracer

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"testing"
	"time"
)

func TestSerializeDataPipeline(t *testing.T) {
	s1 := newSummary()
	s1.Add(0.1)
	s1.Add(0.15)
	s1.Add(0.12)
	s1.Add(0.11)
	for i := 0; i < 25; i++ {
		s1.Add(0.117)
	}
	s2 := newSummary()
	s2.Add(8)
	now := time.Now()
	pipeline := dataPipeline{
		callTime: now,
		latencies: []ddtrace.PipelineLatency{
			{
				Hash: 1,
				Summary: s1,
			},
			// {
		// 		Hash: 2,
		// 		Summary: s2,
		// 	},
		},
	}
	data, err := pipeline.ToBaggage()
	assert.Nil(t, err)
	fmt.Printf("len of baggage is %d\n", len(data))
	tracer := tracer{config: &config{serviceName: "service"}}
	convertedPipeline, err := tracer.DataPipelineFromBaggage(data)
	assert.Nil(t, err)
	assert.Equal(t, pipeline.callTime.Truncate(time.Millisecond).UnixNano(), convertedPipeline.GetCallTime().UnixNano())
	convertedLatencies := convertedPipeline.GetLatencies()
	assert.Equal(t, len(pipeline.latencies), len(convertedLatencies))
	for i, l := range pipeline.latencies {
		assert.Equal(t, l.Hash, convertedLatencies[i].Hash)
		assert.InEpsilonf(t, l.Summary.GetSum(), convertedLatencies[i].Summary.GetSum(), 0.0001, "")
	}
}
