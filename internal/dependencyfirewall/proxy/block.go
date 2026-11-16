package proxy

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
	"unicode/utf8"

	"gitlab.com/gitlab-org/cli/internal/text"
)

// firewallHeader is the response header the proxy sets on a synthesized
// block. It is also the request header the proxy injects on allowed
// upstream traffic (value "allowed" or a decision ID).
const firewallHeader = "X-Gitlab-Dependency-Firewall"

// checkFailedReason replaces a failed policy check's reason, which is the raw
// API error with its URLs and server text, in the response the package
// manager prints.
const checkFailedReason = "Dependency Firewall could not evaluate this package"

// maxBlockReasonPhrase caps the status line's reason phrase in bytes; the
// body still carries the full reason.
const maxBlockReasonPhrase = 512

// blockResponse returns a complete raw HTTP/1.1 403 response for a tunnelled
// HTTPS request, shaped like the given ecosystem's native registry error so
// the package manager renders a clean message. The reason is in the body and,
// because Maven, Gradle and pip print the status line rather than the body,
// in the reason phrase too. Plain-HTTP blocks go through net/http, which
// always writes "Forbidden".
func blockResponse(ecosystem, reason string, checkFailed bool) string {
	contentType, body := blockBody(ecosystem, reason)
	var b strings.Builder
	b.WriteString("HTTP/1.1 403 " + blockReasonPhrase(reason, checkFailed) + "\r\n")
	b.WriteString("Content-Type: " + contentType + "\r\n")
	b.WriteString(firewallHeader + ": blocked\r\n")
	b.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n")
	b.WriteString("Connection: close\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}

// blockReasonPhrase is a single-line reason phrase: the reason comes from the
// policy, so a CR or LF in it must not end the status line.
func blockReasonPhrase(reason string, checkFailed bool) string {
	if checkFailed {
		return checkFailedReason
	}
	phrase := "Blocked by GitLab Dependency Firewall"
	if r := text.SanitizeInline(reason); r != "" {
		phrase += ": " + r
	}
	if len(phrase) <= maxBlockReasonPhrase {
		return phrase
	}
	end := maxBlockReasonPhrase
	for end > 0 && !utf8.RuneStart(phrase[end]) {
		end--
	}
	return phrase[:end]
}

// blockBody returns the Content-Type and body bytes for an ecosystem-native
// 403 error carrying reason.
func blockBody(ecosystem, reason string) (string, string) {
	switch ecosystem {
	case "npm", "nuget":
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
