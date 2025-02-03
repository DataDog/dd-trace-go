package moduleB

import "moduleA"

// HelloB returns a greeting that includes moduleA's greeting.
func HelloB() string {
	return "Hello B + " + moduleA.HelloA()
}
