package main

import (
	"fmt"
	"gopkg.in/DataDog/dd-trace-go.v1/pipelines"
	"log"
	"net/http"
	_ "net/http/pprof"
	"sync/atomic"
	"time"
)

func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	pipelines.Start(pipelines.WithService("service-a"))
	defer pipelines.Stop()

	i := int64(0)
	go func() {
		for range time.NewTicker(time.Second).C {
			fmt.Printf("processed %d payloads\n", atomic.SwapInt64(&i, 0))
		}
	}()
	for {
		atomic.AddInt64(&i, 1)
		p := pipelines.New()
		time.Sleep(time.Millisecond * 100)
		p = p.SetCheckpoint("unresolved-stats.v1")
	}
}
