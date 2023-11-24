package dummy

import (
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

const Greeting = "Hello, World!"

func Greet() string {
	return fmt.Sprintf("%s using dd-trace-go %s", Greeting, version.Tag)
}
