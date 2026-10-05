// Datadog App & API Protection: Boomi Groovy policy
// Serialize request headers for the HTTP callout.
// Add this in the onRequest phase, BEFORE the HTTP callout.
//
// Note: JsonBuilder is NOT in the Boomi allow list. Use JsonOutput instead.
import groovy.json.JsonOutput
def headerMap = [:]
request.headers.keySet().each { name -> headerMap[name] = request.headers.get(name) }
context.setAttribute('dd-serialized-headers', JsonOutput.toJson(headerMap))
