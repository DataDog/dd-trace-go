module appsectest

go 1.16

replace (
	github.com/DataDog/dd-trace-go/appsec => ../
	gopkg.in/DataDog/dd-trace-go.v1 => ../../
)

require (
	github.com/stretchr/testify v1.7.0
	gopkg.in/DataDog/dd-trace-go.v1 v1.33.0
)
