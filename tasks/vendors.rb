desc 'Initialize the development environment'
task :init do
  sh 'go get -u github.com/golang/dep/cmd/dep'
  sh 'go get -u github.com/alecthomas/gometalinter'
  sh 'gometalinter --install'

  sh "go get -t -v ./contrib/..."
  sh "go get -v github.com/opentracing/opentracing-go"
end

namespace :vendors do
  desc "Update the vendors list"
  task :update do
    # download and update our vendors
    sh 'dep ensure'
  end
end
