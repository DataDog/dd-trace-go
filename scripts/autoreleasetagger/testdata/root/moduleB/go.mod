module example.com/root/moduleB

go 1.24.0

toolchain go1.24.1

require (
	example.com/root v0.0.0
	example.com/root/moduleA v0.0.0
)

replace example.com/root => ./..

replace example.com/root/moduleA => ../moduleA
