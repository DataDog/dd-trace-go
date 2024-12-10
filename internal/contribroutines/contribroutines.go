package contribroutines

import "sync"

var stop chan struct{} = make(chan struct{})
var once sync.Once

func Stop() {
	once.Do(func() {
		close(stop)
	})
}

func GetStopChan() chan struct{} {
	return stop
}
