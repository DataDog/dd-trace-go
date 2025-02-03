package moduleA

import "root"

// HelloA returns a greeting that includes the root module's greeting.
func HelloA() string {
	return "Hello A + " + root.HelloRoot()
}
