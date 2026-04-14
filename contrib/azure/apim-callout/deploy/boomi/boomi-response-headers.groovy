// Datadog App & API Protection: Boomi Groovy policy
// Serialize response headers for the HTTP callout.
// Add this in the onResponse phase, BEFORE the response callout.
import groovy.json.JsonOutput
def headerMap = [:]
response.headers.keySet().each { name -> headerMap[name] = response.headers.get(name) }
context.setAttribute('dd-serialized-response-headers', JsonOutput.toJson(headerMap))
