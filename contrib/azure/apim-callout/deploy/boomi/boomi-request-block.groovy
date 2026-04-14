// Datadog App & API Protection: Boomi Groovy policy
// Block requests based on the callout decision.
// Add this in the onRequest phase, AFTER the HTTP callout.
//
// Uses PolicyResult to short-circuit the request chain and return a custom
// block response. Direct response property setters are not allowed by the
// Groovy sandbox in Boomi's Gravitee build.
if (context.getAttribute('dd-action') == 'block') {
    def statusCode = context.getAttribute('dd-block-status') as int
    def body = context.getAttribute('dd-block-body')
    def decodedBody = body != null ? new String(body.decodeBase64()) : ''
    def headersJson = context.getAttribute('dd-block-headers')
    def contentType = 'application/json'
    if (headersJson) {
        def headers = new groovy.json.JsonSlurper().parseText(headersJson)
        if (headers['Content-Type']) {
            contentType = headers['Content-Type'][0]
        }
    }
    result.state = io.gravitee.policy.groovy.PolicyResult.State.FAILURE
    result.code = statusCode
    result.error = decodedBody
    result.contentType = contentType
}
