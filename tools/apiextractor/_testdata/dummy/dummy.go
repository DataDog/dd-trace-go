package dummy

// DummyFunc is a dummy exported function
func DummyFunc() {
	u := DummyStruct{
		unexportedField: 0,
	}
	u.unexportedMethod()
}

// DummyStruct is a dummy exported struct
type DummyStruct struct {
	// ExportedField is an exported field
	ExportedField   string
	unexportedField int
}

// ExportedMethod is an exported method
func (d DummyStruct) ExportedMethod() {}

// unexportedMethod is an unexported method
func (d DummyStruct) unexportedMethod() {}

// AnotherExportedMethod is another exported method
func (d DummyStruct) AnotherExportedMethod() {}

// DummyInterface is a dummy exported interface
type DummyInterface interface {
	// ExportedMethod is an exported method
	ExportedMethod()
}

// DummyFuncWithParams is a dummy exported function with parameters
func DummyFuncWithParams(_ int, _ string) {
	dummyUnexportedFunc()
}

// dummyUnexportedFunc is an unexported function
func dummyUnexportedFunc() {}
