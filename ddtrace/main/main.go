package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"
	_ "net/http/pprof"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	ctx := context.Background()
	tracer.Start(tracer.WithService("piotr-test-service"))
	defer tracer.Stop()
	i := int64(0)
	go func() {
		for range time.NewTicker(time.Second).C {
			fmt.Printf("processed %d payloads\n", atomic.SwapInt64(&i, 0))
		}
	}()
	for {
		atomic.AddInt64(&i, 1)
		_, ctx = tracer.SetDataPipelineCheckpointFromContext(ctx, "queue")
		dataPipeline, ok := tracer.DataPipelineFromContext(ctx)
		if ok {
			if baggage, err := dataPipeline.ToBaggage(); err == nil {
				convertedContext := context.Background()
				if pipeline, err := tracer.DataPipelineFromBaggage(baggage); err == nil {
					convertedContext = tracer.ContextWithDataPipeline(convertedContext, pipeline)
					_, ctx = tracer.SetDataPipelineCheckpointFromContext(convertedContext, "queue2")
				}
			}
		}
	}
}
