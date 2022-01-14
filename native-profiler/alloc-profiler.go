package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/sirupsen/logrus"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler"
)

func main() {
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:          true,
		TimestampFormat:        "2006-01-02T15:04:05Z",
		DisableLevelTruncation: true,
	})
	logrus.SetLevel(6)

	pidPtr := flag.Int("pid", -1, "pid of process")
	flag.Parse()
	pid := *pidPtr
	if pid == -1 {
		log.Fatal("PID argument is mandatory")
		os.Exit(1)
	}

	err := profiler.Start(
		profiler.WithService("alloc_profiler_proto"),
		profiler.WithProfileTypes(
			profiler.NativeHeapProfile,
		),
		profiler.WithPid(pid),
		profiler.WithHeapDuration(1*time.Second),
		profiler.WithPeriod(2*time.Second),
		profiler.WithAPIKey(os.Getenv("DD_API_KEY")),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer profiler.Stop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)
	<-sig
}
