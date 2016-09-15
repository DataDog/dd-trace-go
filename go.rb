def go_build(package, opts={})
  default_cmd = "go build -a"
  if ENV["INCREMENTAL_BUILD"] then
    default_cmd = "go build -i"
  end
  opts = {
    :cmd => default_cmd
  }.merge(opts)

  dd = 'main'
  commit = `git rev-parse --short HEAD`.strip
  branch = `git rev-parse --abbrev-ref HEAD`.strip
  date = `date`.strip
  goversion = `go version`.strip
  traceversion = ENV["TRACE_GO_VERSION"] || "0.1.0"

  ldflags = {
    "#{dd}.BuildDate" => "#{date}",
    "#{dd}.GitCommit" => "#{commit}",
    "#{dd}.GitBranch" => "#{branch}",
    "#{dd}.GoVersion" => "#{goversion}",
    "#{dd}.Version" => "#{traceversion}",
  }.map do |k,v|
    if goversion.include?("1.4")
      "-X #{k} '#{v}'"
    else
      "-X '#{k}=#{v}'"
    end
  end.join ' '

  cmd = opts[:cmd]
  sh "#{cmd} -ldflags \"#{ldflags}\" #{package}"
end

def go_fmt(path)
  out = `go fmt #{path}`
  errors = out.split("\n")
  if errors.length > 0
    puts out
    fail
  end
end

def go_lint(path)
  out = `golint #{path}/*.go`
  errors = out.split("\n")
  puts "#{errors.length} linting issues found"
  if errors.length > 0
    puts out
    fail
  end
end

def go_vet(path)
  sh "go vet #{path}"
end

def go_test_race_condition(path)
  sh "go test -race #{path}"
end

def go_test(profile, path)
  sh "go test -short -covermode=count -coverprofile=#{profile} #{path}"
end

def go_benchmark(path)
  sh "go test -run=NONE -bench=. -memprofile=mem.out #{path}"
  sh "go test -run=NONE -bench=. -cpuprofile=cpu.out #{path}"
  sh "go test -run=NONE -bench=. -blockprofile=block.out #{path}"
end

def go_pprof_text()
  sh "go tool pprof -text -nodecount=10 -cum ./tracer.test cpu.out"
  sh "go tool pprof -text -nodecount=10 -cum ./tracer.test block.out"
  sh "go tool pprof -text -nodecount=10 -cum -inuse_space ./tracer.test mem.out"
end
