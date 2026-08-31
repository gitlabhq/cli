package proxy

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
)

// firewallHeader is the response header the proxy sets on a synthesized
// block. It is also the request header the proxy injects on allowed
// upstream traffic (value "allowed" or a decision ID).
const firewallHeader = "X-Gitlab-Dependency-Firewall"

// blockResponse returns a complete raw HTTP/1.1 403 response, shaped like
// the given ecosystem's native registry error so the package manager
// renders a clean message. The reason is embedded in the body.
func blockResponse(ecosystem, reason string) string {
	contentType, body := blockBody(ecosystem, reason)
	var b strings.Builder
	b.WriteString("HTTP/1.1 403 Forbidden\r\n")
	b.WriteString("Content-Type: " + contentType + "\r\n")
	b.WriteString(firewallHeader + ": blocked\r\n")
	b.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n")
	b.WriteString("Connection: close\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}

// blockBody returns the Content-Type and body bytes for an ecosystem-native
// 403 error carrying reason.
func blockBody(ecosystem, reason string) (string, string) {
	switch ecosystem {
	case "npm":
		//nolint:forbidigo // serializing a small error body for an HTTP response, not stdout
		payload, _ := json.Marshal(map[string]string{"error": reason})
		return "application/json", string(payload)
	case "pypi", "maven":
		return "text/html", fmt.Sprintf("<h1>403 Forbidden</h1><p>%s</p>", html.EscapeString(reason))
	case "gem":
		return "text/plain", reason
	default:
		return "text/plain", reason
	}
}
