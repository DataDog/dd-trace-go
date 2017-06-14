package tracer

type bulkBuffer struct {
	c chan []*Span
}

func newBulkBuffer(size int) *bulkBuffer {
	return &bulkBuffer{c: make(chan []*Span, size)}
}

func (bb *bulkBuffer) Traces() [][]*Span {
	ret := make([][]*Span, 0, bb.Len())

	for {
		select {
		case trace := <-bb.c:
			ret = append(ret, trace)
		default:
			return ret
		}
	}

	return ret
}

func (bb *bulkBuffer) Push(trace []*Span) {
	select {
	case bb.c <- trace:
	default:
		return
	}
}

func (bb *bulkBuffer) Len() int {
	return len(bb.c)
}
