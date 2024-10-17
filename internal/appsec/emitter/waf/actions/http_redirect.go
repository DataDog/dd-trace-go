// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package actions

import (
	"net/http"
	"path"
	"strings"

	"github.com/mitchellh/mapstructure"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	urlpkg "net/url"
)

// redirectActionParams are the dynamic parameters to be provided to a "redirect_request"
// action type upon invocation
type redirectActionParams struct {
	Location   string `mapstructure:"location,omitempty"`
	StatusCode int    `mapstructure:"status_code"`
}

func init() {
	registerActionHandler("redirect_request", NewRedirectAction)
}

func redirectParamsFromMap(params map[string]any) (redirectActionParams, error) {
	var p redirectActionParams
	err := mapstructure.WeakDecode(params, &p)
	return p, err
}

func newRedirectRequestAction(status int, loc string) *BlockHTTP {
	// Default to 303 if status is out of redirection codes bounds
	if status < http.StatusMultipleChoices || status >= http.StatusBadRequest {
		status = http.StatusSeeOther
	}

	// If location is not set we fall back on a default block action
	if loc == "" {
		return &BlockHTTP{
			Handler:          newBlockHandler(http.StatusForbidden, string(blockedTemplateJSON)),
			StatusCode:       status,
			BlockingTemplate: newManualBlockHandler("json"),
		}
	}
	return &BlockHTTP{
		Handler:          http.RedirectHandler(loc, status),
		StatusCode:       status,
		RedirectLocation: loc,
	}
}

// NewRedirectAction creates an action for the "redirect_request" action type
func NewRedirectAction(params map[string]any) []Action {
	p, err := redirectParamsFromMap(params)
	if err != nil {
		log.Debug("appsec: couldn't decode redirect action parameters")
		return nil
	}
	return []Action{newRedirectRequestAction(p.StatusCode, p.Location)}
}

// HandleRedirectLocationString returns the headers and body to be written to the response when a redirect is needed
// Vendored from net/http/server.go
func HandleRedirectLocationString(oldpath string, url string, statusCode int, method string, h map[string][]string) (map[string][]string, []byte) {
	if u, err := urlpkg.Parse(url); err == nil {
		// If url was relative, make its path absolute by
		// combining with request path.
		// The client would probably do this for us,
		// but doing it ourselves is more reliable.
		// See RFC 7231, section 7.1.2
		if u.Scheme == "" && u.Host == "" {
			if oldpath == "" { // should not happen, but avoid a crash if it does
				oldpath = "/"
			}

			// no leading http://server
			if url == "" || url[0] != '/' {
				// make relative path absolute
				olddir, _ := path.Split(oldpath)
				url = olddir + url
			}

			var query string
			if i := strings.Index(url, "?"); i != -1 {
				url, query = url[:i], url[i:]
			}

			// clean up but preserve trailing slash
			trailing := strings.HasSuffix(url, "/")
			url = path.Clean(url)
			if trailing && !strings.HasSuffix(url, "/") {
				url += "/"
			}
			url += query
		}
	}

	// RFC 7231 notes that a short HTML body is usually included in
	// the response because older user agents may not understand 301/307.
	// Do it only if the request didn't already have a Content-Type header.
	_, hadCT := h["content-type"]
	newHeaders := make(map[string][]string, 2)

	newHeaders["location"] = []string{url}
	if !hadCT && (method == "GET" || method == "HEAD") {
		newHeaders["content-length"] = []string{"text/html; charset=utf-8"}
	}

	// Shouldn't send the body for POST or HEAD; that leaves GET.
	var body []byte
	if !hadCT && method == "GET" {
		body = []byte("<a href=\"" + htmlEscape(url) + "\">" + http.StatusText(statusCode) + "</a>.\n")
	}

	return newHeaders, body
}

// Vendored from net/http/server.go
var htmlReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	// "&#34;" is shorter than "&quot;".
	`"`, "&#34;",
	// "&#39;" is shorter than "&apos;" and apos was not in HTML until HTML5.
	"'", "&#39;",
)

// htmlEscape escapes special characters like "<" to become "&lt;".
func htmlEscape(s string) string {
	return htmlReplacer.Replace(s)
}
