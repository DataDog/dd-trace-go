module example.com/root/moduleB

go 1.21

// This is required for the tests to work.
require example.com/root v0.0.0

replace example.com/root => ../
// replace example.com/root/moduleA => ../moduleA
