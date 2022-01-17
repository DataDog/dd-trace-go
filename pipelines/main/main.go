package main

import (
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/pipelines"
)

func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	pipelines.Start(pipelines.WithService("service-a"), pipelines.WithAPIKey(os.Getenv("DD_API_KEY")), pipelines.WithSite(os.Getenv("DD_SITE")))
	// pipelines.Start(pipelines.WithService("service-a"))
	defer pipelines.Stop()

	i := int64(0)
	go func() {
		for range time.NewTicker(time.Second).C {
			fmt.Printf("processed %d payloads\n", atomic.SwapInt64(&i, 0))
		}
	}()
	for {
		atomic.AddInt64(&i, 1)
		p := pipelines.NewPathway()
		time.Sleep(time.Millisecond * 100)
		p = p.SetCheckpoint("edge-name")
	}
}
