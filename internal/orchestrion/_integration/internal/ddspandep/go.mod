// A module outside dd-trace-go's paths with a //dd:span function, standing in
// for a third-party library. otelc must expand the directive in dependencies,
// not only in the module being built.
module example.com/ddspandep

go 1.26.0
