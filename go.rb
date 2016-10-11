def go_test(profile, path)
  sh "go test -short -covermode=count -coverprofile=#{profile} #{path}"
end

def go_benchmark(path)
  sh "go test -run=NONE -bench=. -memprofile=mem.out -cpuprofile=cpu.out -blockprofile=block.out #{path}"
end

def go_pprof_text()
  sh "go tool pprof -text -nodecount=10 -cum ./tracer.test cpu.out"
  sh "go tool pprof -text -nodecount=10 -cum ./tracer.test block.out"
  sh "go tool pprof -text -nodecount=10 -cum -inuse_space ./tracer.test mem.out"
end
