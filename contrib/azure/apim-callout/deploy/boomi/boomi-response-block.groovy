// Datadog App & API Protection: Boomi Groovy policy
// Block responses based on the callout decision.
// Add this in the onResponse phase, AFTER the response callout.
//
// Uses PolicyResult to short-circuit the response chain and return a custom
// block response. Direct response property setters are not allowed by the
// Groovy sandbox in Boomi's Gravitee build.
// Attribute names must match boomi-response-callout.json (dd-resp-block / dd-resp-block-status
// / dd-resp-block-headers / dd-resp-block-content). dd-resp-block is null/empty/'null' when no block.
def block = context.getAttribute('dd-resp-block')
if (block != null && block != '' && block != 'null') {
    def statusCode = context.getAttribute('dd-resp-block-status') as int
    def body = context.getAttribute('dd-resp-block-content')
    def decodedBody = body != null ? new String(body.decodeBase64()) : ''
    def headersJson = context.getAttribute('dd-resp-block-headers')
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
