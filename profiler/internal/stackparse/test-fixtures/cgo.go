//+build ignore

package main

import (
	"runtime/pprof"
	/*
		#include <unistd.h>

		extern int goSleep(int usec);

		static void __attribute__ ((noinline)) c_sleep(int usec) {
			usleep(usec);
		}

		static void c_sleep_loop(int usec) {
			while (1) {
				c_sleep(usec);
			}
		}

		static void c_go_sleep_loop(int usec) {
			while (1) {
				goSleep(usec);
			}
		}
	*/
	"C"
)
import (
	"os"
	"time"
)

func main() {
	go cSleepLoop(time.Second)
	go cGoSleepLoop(time.Second)

	time.Sleep(time.Second / 10)
	pprof.Lookup("goroutine").WriteTo(os.Stdout, 2)
}

func cSleepLoop(d time.Duration) {
	C.c_sleep_loop(C.int(d.Microseconds()))
}

func cGoSleepLoop(d time.Duration) {
	C.c_go_sleep_loop(C.int(d.Microseconds()))
}

//export goSleep
func goSleep(usec C.int) C.int {
	time.Sleep(time.Duration(usec) * time.Microsecond)
	return 0
}
