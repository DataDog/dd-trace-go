# How use this suport for AMP to datadog at gearbox

In the file example_test you have a complete example, the magic do in the line 17

```go
/*
   	//Finaly if you would like recover to ctxSpan of Datadog, in your handler you should do this example:
*/

func HandleExample(ctx gearbox.Context){

    localCtxSpan := ctx.GetLocal("ctxspan")
	ctxSpan, ok := localCtxSpan.(context.Context)
}

// That is, in to variable ctxSpan you have ctxSpan to Datadog. Is necesary previously that you call to middleware provist by this package.
```