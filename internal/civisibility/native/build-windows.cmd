@echo off
setlocal
set GOOS=windows
set GOARCH=amd64
set CC=x:\mingw64\bin\x86_64-w64-mingw32-gcc.exe
set CGO_ENABLED=1
set CGO_CFLAGS=-O2 -g
set CGO_CPPFLAGS=
set CGO_CXXFLAGS=-O2 -g
set CGO_FFLAGS=-O2 -g
set CGO_LDFLAGS=-O2 -g
set CGO_CFLAGS_ALLOW=".*"
set CGO_LDFLAGS_ALLOW=".*"

echo Building windows static library
go build -tags civisibility_native -buildmode=c-archive -o ./output/windows-x64-libtestoptimization-static/testoptimization.lib exports.go main.go

echo Building windows shared library
go build -tags civisibility_native -buildmode=c-shared -o ./output/windows-x64-libtestoptimization-dynamic/testoptimization.dll exports.go main.go

docker build --platform linux/amd64 --build-arg GOARCH=amd64 --build-arg FILE_NAME=windows-x64-libtestoptimization -t win-libtestoptimization-builder:amd64 -f ./Dockerfile-windows .
docker run --rm --mount type=bind,src=.\output,dst=/libtestoptimization win-libtestoptimization-builder:amd64

endlocal