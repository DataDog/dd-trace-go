require 'rake/clean'
require './go'

ORG_PATH="github.com/DataDog"
REPO_PATH="#{ORG_PATH}/dd-trace-go"
TARGETS = %w[
  ./tracer
]

CLOBBER.include("*.cov")

task default: %w[test build]

desc "Run go fmt on #{TARGETS}"
task :fmt do
  TARGETS.each do |t|
    go_fmt(t)
  end
end

desc "Run golint on #{TARGETS}"
task :lint do
  TARGETS.each do |t|
    go_lint(t)
  end
end

desc "Run go vet on #{TARGETS}"
task :vet do
  TARGETS.each do |t|
    go_vet(t)
  end
end

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

desc "Run testsuite"
task :test => %w[fmt lint vet] do
  PROFILE = "profile.cov"  # collect global coverage data in this file
  `echo "mode: count" > #{PROFILE}`

  TARGETS.each do |pkg_folder|
    next if Dir.glob(File.join(pkg_folder, "*.go")).length == 0  # folder is a package if contains go modules
    profile_tmp = "#{pkg_folder}/profile.tmp"  # temp file to collect coverage data
    go_test(profile_tmp, pkg_folder)
    go_test_race_condition(pkg_folder)
    if File.file?(profile_tmp)
      `cat #{profile_tmp} | tail -n +2 >> #{PROFILE}`
      File.delete(profile_tmp)
    end
  end

  sh("go tool cover -func #{PROFILE}")
end

desc "Build dd-trace-go"
task :build do
  TARGETS.each do |t|
    go_build(t)
  end
end
