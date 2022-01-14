package main

import (
	"flag"
	"log"
	"os"

	"github.com/sirupsen/logrus"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler"
)

// todo CLI options
func main() {
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:          true,
		TimestampFormat:        "2006-01-02T15:04:05Z",
		DisableLevelTruncation: true,
	})

	pidPtr := flag.Int("pid", -1, "pid of process")
	flag.Parse()
	pid := *pidPtr
	if pid == -1 {
		pid = os.Getpid()
	}

	err := profiler.Start(
		profiler.WithService("alloc_profiler_proto"),
		profiler.WithProfileTypes(
			profiler.NativeHeapProfile,
		),
		profiler.WithPid(pid),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer profiler.Stop()
}
