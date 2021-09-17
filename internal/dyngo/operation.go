package dyngo

type (
	InstrumentationDescriptor struct {
		Title           string
		Instrumentation Instrumentation
	}

	Instrumentation interface{ isInstrumentation() }

	OperationInstrumentation struct {
		EventListener EventListener
	}

	FunctionInstrumentation struct {
		Symbol   string
		Prologue interface{}
	}
)

func (OperationInstrumentation) isInstrumentation() {}
func (FunctionInstrumentation) isInstrumentation()  {}

func Register(descriptors ...InstrumentationDescriptor) (ids []EventListenerID) {
	for _, desc := range descriptors {
		switch actual := desc.Instrumentation.(type) {
		case OperationInstrumentation:
			ids = append(ids, root.Register(actual.EventListener)...)
		}
	}
	return ids
}

func Unregister(ids []EventListenerID) {
	root.Unregister(ids)
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

var root = newOperation()

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
	if o.parent == nil {
		o.parent = root
	}
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

func (o *Operation) EmitData(data interface{}) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.disabled {
		return
	}
	forEachOperation(o, func(op *Operation) {
		op.emitDataEvent(o, data)
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
