package iostreams

import (
	"encoding/json"
	"reflect"
)

// PrintJSON marshals v to JSON and writes it to stdout. If v is a nil slice,
// it converts it to an empty slice so that JSON marshaling produces [] instead
// of null. This addresses the issue where gitlab.ScanAndCollect returns nil for
// empty results, which would otherwise marshal as null instead of [].
//
// Nested slices within the data structure are left as-is to preserve the
// semantic difference between absent fields (null) and empty arrays ([]) in
// the original API response.
//
// When IOStreams.JQ is active (a --jq expression was supplied), the value is
// passed through the filter before being written.
func (s *IOStreams) PrintJSON(v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		v = reflect.MakeSlice(rv.Type(), 0, 0).Interface()
	}

	if s.JQ != nil && s.JQ.IsActive() {
		return s.JQ.Apply(s.StdOut, v)
	}

	encoder := json.NewEncoder(s.StdOut) //nolint:forbidigo // this is the PrintJSON helper itself
	return encoder.Encode(v)
}

// The message is nested under "error" so it cannot be mistaken for the
// command's success payload, which shares the stream, and so fields can be
// added later without changing the type of an existing one.
type jsonErrorEnvelope struct {
	Error jsonErrorBody `json:"error"`
}

type jsonErrorBody struct {
	Message string `json:"message"`
}

// PrintJSONError writes a machine-readable description of err to stdout for a
// command asked for JSON output that failed before printing anything.
//
// Unlike PrintJSON it does not apply --jq: that filter targets the command's
// success output, so running it here would report a filter failure instead of
// the failure the user needs to see.
func (s *IOStreams) PrintJSONError(err error) error {
	encoder := json.NewEncoder(s.StdOut) //nolint:forbidigo // this is the PrintJSON helper for errors
	return encoder.Encode(jsonErrorEnvelope{Error: jsonErrorBody{Message: err.Error()}})
}
