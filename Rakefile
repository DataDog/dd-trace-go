require 'rake/clean'
require './go'

ORG_PATH="github.com/DataDog"
REPO_PATH="#{ORG_PATH}/dd-trace-go"
TARGETS = %w[
  ./tracer
]

CLOBBER.include("*.cov")

desc "Run benchmarks on #{TARGETS} and output profiles"
task :benchmark do
  TARGETS.each do |t|
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
  profile = "profile.cov"  # collect global coverage data in this file
  `echo "mode: count" > #{profile}`

  TARGETS.each do |pkg_folder|
    next if Dir.glob(File.join(pkg_folder, "*.go")).length == 0  # folder is a package if contains go modules
    profile_tmp = "#{pkg_folder}/profile.tmp"  # temp file to collect coverage data
    go_test(profile_tmp, pkg_folder)
    if File.file?(profile_tmp)
      `cat #{profile_tmp} | tail -n +2 >> #{profile}`
      File.delete(profile_tmp)
    end
  end
  sh("go tool cover -func #{profile}")
end

desc "Build dd-trace-go"
task :build do
  TARGETS.each do |t|
    go_build(t)
  end
end

task :get do
  sh "go get ./..."
end

task :ci => [:get, :'lint:errors', :cover, :test, :race]

namespace :lint do

  # a few options for the slow cmds
  #  - gotype is a bit of a pain to run
  #  - dupl had only false positives
  slow_opts = "--deadline 60s --disable dupl --disable gotype"

  task :install do
    sh "go get -u github.com/alecthomas/gometalinter"
    sh "gometalinter --install"
  end

  desc "Lint the fast things"
  task :fast do
    enabled = %w{govet errcheck gofmt}
    enabled_opts = enabled.map{|e| "--enable=#{e}"}.join(" ")
    sh "gometalinter --disable-all #{enabled_opts} --deadline=5s ./..."
  end

  desc "Lint everything"
  task :errors => :install do
    sh "gometalinter --errors #{slow_opts} ./..."
  end

  desc "Lint everything with warnings"
  task :warn => :install do
    sh "gometalinter #{slow_opts} ./..."
  end

end

task :lint => :'lint:fast'

task :default => [:test, :lint]
