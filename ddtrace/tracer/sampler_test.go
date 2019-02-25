package tracer

import (
	"io/ioutil"
	"math"
	"strings"
	"sync"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

	"github.com/stretchr/testify/assert"
)

func TestPrioritySampler(t *testing.T) {
	// create a new span with given service/env
	mkSpan := func(svc, env string) *span {
		s := &span{Service: svc, Meta: map[string]string{}}
		if env != "" {
			s.Meta["env"] = env
		}
		return s
	}

	t.Run("mkspan", func(t *testing.T) {
		assert := assert.New(t)
		s := mkSpan("my-service", "my-env")
		assert.Equal("my-service", s.Service)
		assert.Equal("my-env", s.Meta[ext.Environment])

		s = mkSpan("my-service2", "")
		assert.Equal("my-service2", s.Service)
		_, ok := s.Meta[ext.Environment]
		assert.False(ok)
	})

	t.Run("ops", func(t *testing.T) {
		ps := newPrioritySampler()
		assert := assert.New(t)

		type key struct{ service, env string }
		for _, tt := range []struct {
			in  string
			out map[key]float64
		}{
			{
				in: `{}`,
				out: map[key]float64{
					key{"some-service", ""}:       1,
					key{"obfuscate.http", "none"}: 1,
				},
			},
			{
				in: `{
					"rate_by_service":{
						"service:,env:":0.8,
						"service:obfuscate.http,env:":0.9,
						"service:obfuscate.http,env:none":0.9
					}
				}`,
				out: map[key]float64{
					key{"obfuscate.http", ""}:      0.9,
					key{"obfuscate.http", "none"}:  0.9,
					key{"obfuscate.http", "other"}: 0.8,
					key{"some-service", ""}:        0.8,
				},
			},
			{
				in: `{
					"rate_by_service":{
						"service:my-service,env:":0.2,
						"service:my-service,env:none":0.2
					}
				}`,
				out: map[key]float64{
					key{"my-service", ""}:          0.2,
					key{"my-service", "none"}:      0.2,
					key{"obfuscate.http", ""}:      0.8,
					key{"obfuscate.http", "none"}:  0.8,
					key{"obfuscate.http", "other"}: 0.8,
					key{"some-service", ""}:        0.8,
				},
			},
		} {
			assert.NoError(ps.readRatesJSON(ioutil.NopCloser(strings.NewReader(tt.in))))
			for k, v := range tt.out {
				assert.Equal(v, ps.getRate(mkSpan(k.service, k.env)), k)
			}
		}
	})

	t.Run("race", func(t *testing.T) {
		ps := newPrioritySampler()
		assert := assert.New(t)

		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				assert.NoError(ps.readRatesJSON(
					ioutil.NopCloser(strings.NewReader(
						`{
							"rate_by_service":{
								"service:,env:":0.8,
								"service:obfuscate.http,env:none":0.9
							}
						}`,
					)),
				))
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				ps.getRate(mkSpan("obfuscate.http", "none"))
				ps.getRate(mkSpan("other.service", "none"))
			}
		}()

		wg.Wait()
	})

	t.Run("apply", func(t *testing.T) {
		ps := newPrioritySampler()
		assert := assert.New(t)
		assert.NoError(ps.readRatesJSON(
			ioutil.NopCloser(strings.NewReader(
				`{
					"rate_by_service":{
						"service:obfuscate.http,env:":0.5,
						"service:obfuscate.http,env:none":0.5
					}
				}`,
			)),
		))

		testSpan1 := newBasicSpan("http.request")
		testSpan1.Service = "obfuscate.http"
		testSpan1.TraceID = math.MaxUint64 - (math.MaxUint64 / 4)

		ps.apply(testSpan1)
		assert.EqualValues(ext.PriorityAutoKeep, testSpan1.Metrics[keySamplingPriority])
		assert.EqualValues(0.5, testSpan1.Metrics[keySamplingPriorityRate])

		testSpan1.TraceID = math.MaxUint64 - (math.MaxUint64 / 3)
		ps.apply(testSpan1)
		assert.EqualValues(ext.PriorityAutoReject, testSpan1.Metrics[keySamplingPriority])
		assert.EqualValues(0.5, testSpan1.Metrics[keySamplingPriorityRate])

		testSpan1.Service = "other-service"
		testSpan1.TraceID = 1
		assert.EqualValues(ext.PriorityAutoReject, testSpan1.Metrics[keySamplingPriority])
		assert.EqualValues(0.5, testSpan1.Metrics[keySamplingPriorityRate])
	})
}

func TestRateSampler(t *testing.T) {
	assert := assert.New(t)
	assert.True(NewRateSampler(1).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0).Sample(newBasicSpan("test")))
	assert.False(NewRateSampler(0.99).Sample(internal.NoopSpan{}))
}

func TestRateSamplerSetting(t *testing.T) {
	assert := assert.New(t)
	rs := NewRateSampler(1)
	assert.Equal(float64(1), rs.Rate())
	rs.SetRate(0.5)
	assert.Equal(float64(0.5), rs.Rate())
}
