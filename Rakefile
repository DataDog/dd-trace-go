require 'rake/clean'
require './go'


CLOBBER.include("*.cov")

def packages
   return `go list ./...`.split("\n")
end

desc "Run benchmarks"
task :benchmark do
  packages.each do |t|
    go_benchmark(t)
  end
end

desc "Run pprof available profiles"
task :pprof do
  go_pprof_text()
end

desc "test"
task :test do
  sh "go test ./..."
end

desc "test race"
task :race do
  sh "go test -race ./..."
end

desc "Run coverage report"
task :cover do
  profile = "/tmp/dd-trace-go-profile.cov"  # collect global coverage data in this file

  `echo "mode: count" > #{profile}`
  packages().each do |pkg_folder|
    profile_tmp = "/tmp/profile.tmp"  # temp file to collect coverage data
    go_test(profile_tmp, pkg_folder)
    if File.file?(profile_tmp)
      `cat #{profile_tmp} | tail -n +2 >> #{profile}`
      File.delete(profile_tmp)
    end
  end

  sh("go tool cover -func #{profile}")
end

task :get do
  sh "go get -t ./..."
end

task :ci => [:get, :'lint:errors', :cover, :test, :race]

namespace :lint do

  # a few options for the slow cmds
  #  - gotype is a bit of a pain to run
  #  - dupl had only false positives
  disable = "--disable dupl --disable gotype"

  desc "install metalinters"
  task :install do
    sh "go get -u github.com/alecthomas/gometalinter"
    sh "gometalinter --install"
  end

  desc "Lint the fast things"
  task :fast do
    sh "gometalinter --fast #{disable} --deadline=5s ./..."
  end

  desc "Lint everything"
  task :errors do
    sh "gometalinter --deadline 60s --errors #{disable} ./..."
  end

  desc "Lint everything with warnings"
  task :warn do
    sh "gometalinter --deadline 60s #{disable} ./..."
  end

end

task :lint => :'lint:fast'

task :default => [:test, :lint]
