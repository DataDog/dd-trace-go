package validationtest

var testCaseV0 = map[string]string{
	"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA": "v0",
}

var testCaseV1 = map[string]string{
	"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA": "v1",
}

func getTestCases() [2]map[string]string {
	var testCases [2]map[string]string
	testCases[0] = testCaseV0
	testCases[1] = testCaseV1
	return testCases
}
