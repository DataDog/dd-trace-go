desc 'Initialize the development environment'
task :init do
  sh 'go get -u github.com/tools/godep'
  sh 'go get -u github.com/alecthomas/gometalinter'
  sh 'gometalinter --install'

  sh "go get -v ./contrib/..."
end

namespace :vendors do
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
