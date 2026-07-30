package iostreams

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newIOWithJQ returns an IOStreams suitable for tests, with a fresh JQFilter
// already attached so callers can drive it via io.JQ.Set without nil checks.
func newIOWithJQ(buf *bytes.Buffer) *IOStreams {
	return &IOStreams{StdOut: buf, JQ: &JQFilter{}}
}

func TestPrintJSON_TopLevelNilSliceBecomesEmptyArray(t *testing.T) {
	// Test that top-level nil slices (like from gitlab.ScanAndCollect) are
	// normalized to [] instead of null
	type Token struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	buf := &bytes.Buffer{}
	io := newIOWithJQ(buf)

	// Simulate what gitlab.ScanAndCollect returns for empty results
	var tokens []Token // nil slice

	err := io.PrintJSON(tokens)
	require.NoError(t, err)

	// Verify the actual JSON output is []
	assert.Equal(t, "[]\n", buf.String())
}

func TestPrintJSON_TopLevelSliceWithData(t *testing.T) {
	// Test that top-level slices with data are preserved correctly
	type Token struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	buf := &bytes.Buffer{}
	io := newIOWithJQ(buf)

	tokens := []Token{
		{ID: 1, Name: "token1"},
		{ID: 2, Name: "token2"},
	}

	err := io.PrintJSON(tokens)
	require.NoError(t, err)

	// Parse the output
	var result []Token
	jsonBytes := bytes.TrimSpace(buf.Bytes())
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)

	assert.Len(t, result, 2)
	assert.Equal(t, tokens[0].ID, result[0].ID)
	assert.Equal(t, tokens[0].Name, result[0].Name)
	assert.Equal(t, tokens[1].ID, result[1].ID)
	assert.Equal(t, tokens[1].Name, result[1].Name)
}

func TestPrintJSON_NestedNilSlicesPreserved(t *testing.T) {
	// Test that nested nil slices (from API responses) are preserved as null
	// to maintain the semantic difference between absent and empty
	type Token struct {
		ID     int      `json:"id"`
		Scopes []string `json:"scopes"` // nil should stay null in JSON
	}

	buf := &bytes.Buffer{}
	io := newIOWithJQ(buf)

	tokens := []Token{
		{ID: 1, Scopes: nil}, // This nil should remain null in the JSON output
	}

	err := io.PrintJSON(tokens)
	require.NoError(t, err)

	// Verify the nested nil slice is preserved as null
	jsonBytes := bytes.TrimSpace(buf.Bytes())
	var result []map[string]any
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)

	require.Len(t, result, 1)

	// The scopes field should be null (present but nil)
	scopes, exists := result[0]["scopes"]
	assert.True(t, exists, "expected scopes field to exist")
	assert.Nil(t, scopes, "expected scopes to be null")
}

func TestPrintJSON_PassthroughWhenJQNil(t *testing.T) {
	// IOStreams instances constructed without a JQFilter should still work;
	// PrintJSON should bypass the filter and emit raw JSON.
	buf := &bytes.Buffer{}
	io := &IOStreams{StdOut: buf} // no JQ

	require.NoError(t, io.PrintJSON(map[string]int{"a": 1}))
	assert.Equal(t, "{\"a\":1}\n", buf.String())
}

func TestPrintJSON_JQFilterSelectsField(t *testing.T) {
	buf := &bytes.Buffer{}
	io := newIOWithJQ(buf)
	require.NoError(t, io.JQ.Set(".name"))

	require.NoError(t, io.PrintJSON(map[string]string{"name": "alpha"}))

	// jq -r style: bare string output.
	assert.Equal(t, "alpha\n", buf.String())
}

func TestPrintJSON_JQFilterEmitsObject(t *testing.T) {
	buf := &bytes.Buffer{}
	io := newIOWithJQ(buf)
	require.NoError(t, io.JQ.Set(".user"))

	require.NoError(t, io.PrintJSON(map[string]any{
		"user": map[string]any{"id": 1, "name": "alpha"},
	}))

	// Non-string results stay JSON-encoded (compact).
	var result map[string]any
	err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &result)
	require.NoError(t, err)
	assert.Equal(t, "alpha", result["name"])
	assert.EqualValues(t, 1, result["id"])
}

func TestPrintJSON_JQFilterIteratesArray(t *testing.T) {
	buf := &bytes.Buffer{}
	io := newIOWithJQ(buf)
	require.NoError(t, io.JQ.Set(".[].name"))

	input := []map[string]string{
		{"name": "alpha"},
		{"name": "beta"},
	}
	require.NoError(t, io.PrintJSON(input))

	assert.Equal(t, "alpha\nbeta\n", buf.String())
}

func TestPrintJSON_JQFilterMissingFieldEmitsNull(t *testing.T) {
	// Edge case: jq selecting a missing field emits "null", matching jq semantics.
	buf := &bytes.Buffer{}
	io := newIOWithJQ(buf)
	require.NoError(t, io.JQ.Set(".nonexistent"))

	require.NoError(t, io.PrintJSON(map[string]string{"name": "alpha"}))

	assert.Equal(t, "null\n", buf.String())
}

func TestPrintJSON_JQFilterEmptyProducesNoOutput(t *testing.T) {
	// Edge case: `empty` consumes the input and yields no results.
	buf := &bytes.Buffer{}
	io := newIOWithJQ(buf)
	require.NoError(t, io.JQ.Set("empty"))

	require.NoError(t, io.PrintJSON(map[string]string{"name": "alpha"}))

	assert.Empty(t, buf.String())
}

func TestPrintJSON_JQFilterRuntimeErrorIsWrapped(t *testing.T) {
	// Edge case: a runtime error from gojq (e.g. tonumber on a non-numeric
	// string) is surfaced as a wrapped error with the --jq prefix.
	buf := &bytes.Buffer{}
	io := newIOWithJQ(buf)
	require.NoError(t, io.JQ.Set(".foo | tonumber"))

	err := io.PrintJSON(map[string]string{"foo": "bar"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--jq error")
}

func TestPrintJSONError_WritesEnvelopeToStdout(t *testing.T) {
	buf := &bytes.Buffer{}
	io := newIOWithJQ(buf)

	require.NoError(t, io.PrintJSONError(errors.New("404 Not Found")))

	var got struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "404 Not Found", got.Error.Message)
}

func TestPrintJSONError_IgnoresJQFilter(t *testing.T) {
	// The --jq expression targets the command's success output, so applying it
	// to an error object would replace the failure with a filter error.
	buf := &bytes.Buffer{}
	io := newIOWithJQ(buf)
	require.NoError(t, io.JQ.Set(".[].id"))

	require.NoError(t, io.PrintJSONError(errors.New("404 Not Found")))

	var got struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "404 Not Found", got.Error.Message)
}

func TestPrintJSONError_PassthroughWhenJQNil(t *testing.T) {
	buf := &bytes.Buffer{}
	io := &IOStreams{StdOut: buf} // no JQ

	require.NoError(t, io.PrintJSONError(errors.New("boom")))
	assert.JSONEq(t, `{"error":{"message":"boom"}}`, buf.String())
}
