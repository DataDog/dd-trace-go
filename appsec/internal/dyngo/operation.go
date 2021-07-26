package dyngo

type InstrumentationDescriptor struct {
}

func Register(InstrumentationDescriptor) {

}

type (
	Option interface {
		apply(s *Operation)
	}

	optionFunc func(s *Operation)
)

func (f optionFunc) apply(s *Operation) {
	f(s)
}

func WithParent(parent *Operation) Option {
	return optionFunc(func(op *Operation) {
		op.parent = parent
	})
}

type Operation struct {
	parent *Operation
	eventManager
}

func newOperation(opts ...Option) *Operation {
	op := &Operation{}
	for _, opt := range opts {
		opt.apply(op)
	}
	return op
}

func StartOperation(args interface{}, opts ...Option) *Operation {
	o := newOperation(opts...)
	forEachParentOperation(o, func(op *Operation) {
		op.emitStartEvent(o, args)
	})
	return o
}

func (o *Operation) Parent() *Operation {
	return o.parent
}

func (o *Operation) Finish(results interface{}) {
	defer o.disable()
	forEachOperation(o, func(op *Operation) {
		op.emitFinishEvent(o, results)
	})
}

func (e *eventManager) disable() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disabled = true
	e.onStart.clear()
	e.onData.clear()
	e.onFinish.clear()
}

func forEachOperation(op *Operation, do func(op *Operation)) {
	for ; op != nil; op = op.Parent() {
		do(op)
	}
}

func forEachParentOperation(op *Operation, do func(op *Operation)) {
	forEachOperation(op.Parent(), do)
}
