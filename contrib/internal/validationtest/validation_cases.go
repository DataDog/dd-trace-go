package validationtest

import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

// ValidationTestCase represents configuration replicating real tracer configuration, where there is a combination
// of environment variables and tracer Start Options
type Case struct {
	EnvVars      map[string]string
	StartOptions []tracer.StartOption
}

var testCaseV0Env = map[string]string{
	"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA": "v0",
}

var testCaseV1Env = map[string]string{
	"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA": "v1",
}

func getEnvConfigs() []map[string]string {
	var envConfigs []map[string]string
	return append(envConfigs, testCaseV0Env, testCaseV1Env)
}

var noConfiguredServiceCase = []tracer.StartOption{}
var userSpecifiedServiceCase = []tracer.StartOption{tracer.WithService("DD-Test-Agent-Trace-Checks-Alt-Name")}

func getStartOptions() [][]tracer.StartOption {
	var startOptions [][]tracer.StartOption
	return append(startOptions, noConfiguredServiceCase, userSpecifiedServiceCase)
}

// GetValidationTestCases creates a matrix of testCases to run, combining the different Env configs with tracer StartOptions
func GetValidationTestCases() []Case {
	envConfigs := getEnvConfigs()
	startOptionConfigs := getStartOptions()
	var validationTestCases []Case

	for _, env := range envConfigs {
		for _, startOptions := range startOptionConfigs {
			testCase := Case{
				EnvVars:      env,
				StartOptions: startOptions,
			}
			validationTestCases = append(validationTestCases, testCase)
		}
	}
	return validationTestCases
}
