require 'rake/clean'

CLOBBER.include("*.cov")


desc "Run benchmarks"
task :benchmark do
  go_packages.each do |t|
    go_benchmark(t)
  end
end

desc "Run pprof available profiles"
task :pprof do
  go_pprof_text()
end

desc "test"
task :test do
  sh "go test ./tracer ./contrib/..."
end

desc "test race"
task :race do
  sh "go test -race ./tracer ./contrib/..."
end

desc "Run coverage report"
task :cover do
  profile = "profile.cov"  # collect global coverage data in this file
  `echo "mode: count" > #{profile}`
  go_packages().each do |pkg_folder|
    profile_tmp = "/tmp/profile.tmp"  # temp file to collect coverage data
    go_test(profile_tmp, pkg_folder)
    if File.file?(profile_tmp)
      `cat #{profile_tmp} | tail -n +2 >> #{profile}`
      File.delete(profile_tmp)
    end
  end

  sh("go tool cover -func #{profile}")
end

desc "Get all required packages"
task :get do
  sh "go get -t ./tracer ./contrib/..."
end

namespace :vendors do
  desc "Install vendoring tools"
  task :install do
    sh "go get github.com/tools/godep"
  end

  desc "Update the vendors list"
  task :update do
    # download and update our vendors
    sh "go get -u github.com/ugorji/go/codec"
    # clean the current folder and start vendoring
    sh "rm -r vendor/"
    sh "godep save ./tracer"
    # remove extra stuff
    sh "rm -r Godeps/"
    sh "rm -r vendor/github.com/stretchr/"
  end
end

task :ci => [:get, :lint, :cover, :test, :race]

namespace :lint do

  # a few options for the slow cmds
  #  - gotype is a bit of a pain to run
  #  - dupl had only false positives
  disable = "--disable dupl --disable gotype"

  desc "install metalinters"
  task :install do
    sh "go get -u github.com/alecthomas/gometalinter"
    sh "cd ~/.go_workspace/src/github.com/alecthomas/gometalinter && git checkout v1.2.1 && go install && cd -"
    sh "gometalinter --install"
  end

  desc "Lint the fast things"
  task :fast do
    sh "gometalinter --fast #{disable} --errors --deadline=10s ./tracer ./contrib/..."
  end

  desc "Lint everything"
  task :errors do
    sh "gometalinter --deadline 60s --errors #{disable} ./tracer ./contrib/..."
  end

  desc "Lint everything with warnings"
  task :warn do
    sh "gometalinter --deadline 60s #{disable} ./tracer ./contrib/..."
  end

end

task :lint => :'lint:fast'

task :default => [:test, :lint]

def go_packages
   return `go list ./tracer ./contrib/...`.split("\n")
end

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
