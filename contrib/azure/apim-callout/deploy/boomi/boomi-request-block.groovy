// Datadog App & API Protection: Boomi Groovy policy
// Block requests based on the callout decision.
// Add this in the onRequest phase, AFTER the HTTP callout.
//
// Uses PolicyResult to short-circuit the request chain and return a custom
// block response. Direct response property setters are not allowed by the
// Groovy sandbox in Boomi's Gravitee build.
// Attribute names must match boomi-request-callout.json (dd-block / dd-block-status
// / dd-block-headers / dd-block-content). dd-block is null/empty/'null' when no block.
def block = context.getAttribute('dd-block')
if (block != null && block != '' && block != 'null') {
    def statusCode = context.getAttribute('dd-block-status') as int
    def body = context.getAttribute('dd-block-content')
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
