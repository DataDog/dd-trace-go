package contribroutines

var stop chan struct{}

func Stop() {
	close(stop)
}

func GetStopChan() chan struct{} {
	return stop
}
