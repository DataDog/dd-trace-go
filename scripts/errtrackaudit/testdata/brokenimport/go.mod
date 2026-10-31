module example.com/brokenimport

go 1.26.0

require example.com/brokendep v0.0.0

replace example.com/brokendep => ../brokendep
