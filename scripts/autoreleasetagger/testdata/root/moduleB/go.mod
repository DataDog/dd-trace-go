module example.com/root/moduleB/v2

go 1.25.0

require (
	example.com/root/v2 v2.0.0
	example.com/root/moduleA/v2 v2.0.0
)

replace example.com/root/v2 => ./..

replace example.com/root/moduleA/v2 => ../moduleA
