package tracer

const (
	traceChanLen   = 1000
	serviceChanLen = 50
	errChanLen     = 200
)

type tracerChans struct {
	trace        chan []*Span
	service      chan Service
	err          chan error
	traceFlush   chan struct{}
	serviceFlush chan struct{}
	errFlush     chan struct{}
}

func newTracerChans() tracerChans {
	return tracerChans{
		trace:        make(chan []*Span, traceChanLen),
		service:      make(chan Service, serviceChanLen),
		err:          make(chan error, errChanLen),
		traceFlush:   make(chan struct{}, 1),
		serviceFlush: make(chan struct{}, 1),
		errFlush:     make(chan struct{}, 1),
	}
}

func (tc *tracerChans) pushTrace(trace []*Span) {
	if len(tc.trace) >= cap(tc.trace)/2 { // starts being full, anticipate, try and flush soon
		select {
		case tc.traceFlush <- struct{}{}:
		default: // a flush was already requested, skip
		}
	}
	tc.trace <- trace // blocking if channel is full, until next flush
}

func (tc *tracerChans) pushService(service Service) {
	if len(tc.service) >= cap(tc.service)/2 { // starts being full, anticipate, try and flush soon
		select {
		case tc.serviceFlush <- struct{}{}:
		default: // a flush was already requested, skip
		}
	}
	tc.service <- service // blocking if channel is full, until next flush
}

func (tc *tracerChans) pushErr(err error) {
	if len(tc.err) >= cap(tc.err)/2 { // starts being full, anticipate, try and flush soon
		select {
		case tc.errFlush <- struct{}{}:
		default: // a flush was already requested, skip
		}
	}
	tc.err <- err // blocking if channel is full, until next flush
}
