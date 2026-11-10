package autoflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	autoflowv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// MarshalValue encodes v as plain JSON: None and a missing value become
// null, an integer or a float a number, a list an array and a dict with string
// keys an object in the dict's order. A float is written like Python's repr
// writes it, with a fraction or an exponent, so it does not read back as an
// integer. A value without a JSON equivalent, such as a channel, a tuple or
// bytes, is encoded in its protobuf JSON form.
func MarshalValue(v *autoflowv1.Value) ([]byte, error) {
	var buf bytes.Buffer
	if err := marshalValue(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MarshalProtoValue encodes v in its protobuf JSON form.
func MarshalProtoValue(v *autoflowv1.Value) ([]byte, error) {
	b, err := protojson.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to encode value: %w", err)
	}
	// protojson randomizes its whitespace, so the output is compacted to stay
	// the same between runs.
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return nil, fmt.Errorf("failed to encode value: %w", err)
	}
	return buf.Bytes(), nil
}

func marshalValue(buf *bytes.Buffer, v *autoflowv1.Value) error {
	switch k := v.GetKind().(type) {
	case nil, *autoflowv1.Value_NoneValue:
		buf.WriteString("null")
	case *autoflowv1.Value_BoolValue:
		buf.WriteString(strconv.FormatBool(k.BoolValue))
	case *autoflowv1.Value_IntegerValue:
		buf.WriteString(strconv.FormatInt(k.IntegerValue, 10))
	case *autoflowv1.Value_FloatValue:
		if math.IsInf(k.FloatValue, 0) || math.IsNaN(k.FloatValue) {
			return marshalProto(buf, v)
		}
		buf.WriteString(formatFloat(k.FloatValue))
	case *autoflowv1.Value_StringValue:
		return marshalString(buf, k.StringValue)
	case *autoflowv1.Value_ListValue:
		buf.WriteByte('[')
		for i, e := range k.ListValue.GetValues() {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := marshalValue(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case *autoflowv1.Value_DictValue:
		if !hasStringKeys(k.DictValue) {
			return marshalProto(buf, v)
		}
		buf.WriteByte('{')
		for i, kv := range k.DictValue.GetKeyValues() {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := marshalString(buf, kv.GetKey().GetStringValue()); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := marshalValue(buf, kv.GetVal()); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return marshalProto(buf, v)
	}
	return nil
}

func formatFloat(f float64) string {
	if a := math.Abs(f); a != 0 && (a < 1e-4 || a >= 1e16) {
		return strconv.FormatFloat(f, 'e', -1, 64)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

func hasStringKeys(d *autoflowv1.DictValue) bool {
	for _, kv := range d.GetKeyValues() {
		if _, ok := kv.GetKey().GetKind().(*autoflowv1.Value_StringValue); !ok {
			return false
		}
	}
	return true
}

func marshalString(buf *bytes.Buffer, s string) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b) //nolint:forbidigo // encoding one string of a value, not producing stdout output
	// A workflow result is read by people and tools, not embedded in HTML.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("failed to encode value: %w", err)
	}
	buf.Write(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
	return nil
}

func marshalProto(buf *bytes.Buffer, v *autoflowv1.Value) error {
	b, err := MarshalProtoValue(v)
	if err != nil {
		return err
	}
	buf.Write(b)
	return nil
}
