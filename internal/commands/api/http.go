package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/api"
)

const (
	// stringArrayRegexPattern represents a pattern to find strings like: [item, item_two]
	stringArrayRegexPattern = `^\[\s*([[:lower:]_]+(\s*,\s*[[:lower:]_]+)*)?\s*\]$`
)

var strArrayRegex = regexp.MustCompile(stringArrayRegexPattern)

func httpRequest(ctx context.Context, client *api.Client, method, p string, params any, headers []string) (*http.Response, error) {
	var err error
	isGraphQL := p == "graphql"

	baseURL := client.Lab().BaseURL()
	baseURLStr := baseURL.String()
	if strings.Contains(p, "://") {
		baseURLStr = p
	} else if isGraphQL {
		baseURL.Path = strings.TrimSuffix(strings.TrimSuffix(baseURL.Path, "/"), "/api/v4") + "/api/graphql"
		baseURLStr = baseURL.String()
	} else {
		baseURLStr = baseURLStr + strings.TrimPrefix(p, "/")
	}

	var body io.Reader
	var bodyIsJSON bool
	switch pp := params.(type) {
	case map[string]any:
		if strings.EqualFold(method, http.MethodGet) || strings.EqualFold(method, http.MethodDelete) {
			baseURLStr, err = parseQuery(baseURLStr, pp)
			if err != nil {
				return nil, err
			}
		} else {
			for key, value := range pp {
				if vv, ok := value.([]byte); ok {
					pp[key] = string(vv)
				}

				if strValue, ok := value.(string); ok && strArrayRegex.MatchString(strValue) {
					pp[key] = parseStringArrayField(strValue)
				}
			}
			if isGraphQL {
				pp = groupGraphQLVariables(pp)
			}

			b, err := json.Marshal(pp) //nolint:forbidigo // building HTTP request body, not stdout
			if err != nil {
				return nil, fmt.Errorf("error serializing parameters: %w", err)
			}
			body = bytes.NewBuffer(b)
			bodyIsJSON = true
		}
	case io.Reader:
		body = pp
	case nil:
		body = nil
	default:
		return nil, fmt.Errorf("unrecognized parameter type: %v", params)
	}

	reqURL, err := url.Parse(baseURLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid request URL: %w", err)
	}
	req, err := api.NewHTTPRequest(ctx, client, method, reqURL, body, headers, bodyIsJSON)
	if err != nil {
		return nil, err
	}
	return client.HTTPClient().Do(req)
}

func groupGraphQLVariables(params map[string]any) map[string]any {
	topLevel := make(map[string]any)
	variables := make(map[string]any)

	for key, val := range params {
		switch key {
		case "query", "operationName":
			topLevel[key] = val
		default:
			variables[key] = val
		}
	}

	if len(variables) > 0 {
		topLevel["variables"] = variables
	}
	return topLevel
}

func parseQuery(path string, params map[string]any) (string, error) {
	if len(params) == 0 {
		return path, nil
	}
	q := url.Values{}
	for key, value := range params {
		switch v := value.(type) {
		case string:
			q.Add(key, v)
		case []byte:
			q.Add(key, string(v))
		case nil:
			q.Add(key, "")
		case int:
			q.Add(key, fmt.Sprintf("%d", v))
		case bool:
			q.Add(key, fmt.Sprintf("%v", v))
		default:
			return "", fmt.Errorf("unknown type %v", v)
		}
	}

	sep := "?"
	if strings.ContainsRune(path, '?') {
		sep = "&"
	}
	return path + sep + q.Encode(), nil
}

// buildMultipartBody constructs a multipart/form-data request body from a slice
// of raw "key=value" or "key=@filepath" strings. File fields use the @filepath
// syntax; all other fields are written as plain text parts. The returned
// contentType string includes the boundary and must be set as the request's
// Content-Type header.
//
// The body is streamed via io.Pipe so that file content is never fully buffered
// in memory.
func buildMultipartBody(formFields []string, stdin io.ReadCloser) (io.Reader, string) {
	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)
	contentType := w.FormDataContentType()

	go func() {
		err := writeMultipartFields(w, formFields, stdin)
		if closeErr := w.Close(); err == nil {
			err = closeErr
		}
		pw.CloseWithError(err)
	}()

	return pr, contentType
}

// writeMultipartFields writes all form fields to w. It is called from a
// goroutine inside buildMultipartBody.
func writeMultipartFields(w *multipart.Writer, formFields []string, stdin io.ReadCloser) error {
	for _, f := range formFields {
		key, value, err := parseField(f)
		if err != nil {
			return err
		}
		if path, isFile := strings.CutPrefix(value, "@"); isFile {
			if err := copyFileField(w, key, path, stdin); err != nil {
				return err
			}
		} else {
			if err := w.WriteField(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyFileField writes a single file part to w. Each call opens and closes its
// own file handle, so file descriptors are not accumulated across loop iterations.
func copyFileField(w *multipart.Writer, key, path string, stdin io.ReadCloser) error {
	var r io.Reader
	var filename string
	if path == "-" {
		r = stdin
		filename = "-"
	} else {
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fh.Close()
		r = fh
		filename = filepath.Base(path)
	}
	fw, err := w.CreateFormFile(key, filename)
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, r)
	return err
}

func parseStringArrayField(strValue string) []string {
	strValue = strings.TrimPrefix(strValue, "[")
	strValue = strings.TrimSuffix(strValue, "]")
	if strings.TrimSpace(strValue) == "" {
		return []string{}
	}
	strArrayElements := strings.Split(strValue, ",")

	var strSlice []string
	for _, element := range strArrayElements {
		element = strings.TrimSpace(element)
		element = strings.Trim(element, `"`)
		strSlice = append(strSlice, element)
	}

	return strSlice
}
