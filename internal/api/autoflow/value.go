package autoflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	autoflowv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/v1"
)

// ParseValue converts a JSON document into an AutoFlow value. null becomes
// None, a number without a fraction or exponent an integer and any other
// number a float, an array a list and an object a dict with string keys in the
// order the document lists them.
func ParseValue(data []byte) (*autoflowv1.Value, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("no JSON value")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, truncated(err)
	}
	switch _, err := dec.Token(); {
	case errors.Is(err, io.EOF):
		return v, nil
	case err != nil:
		return nil, truncated(err)
	default:
		return nil, errors.New("more than one JSON value")
	}
}

// truncated gives a document that ends inside a value one error, where the
// decoder reports io.EOF, io.ErrUnexpectedEOF or a syntax error depending on
// where it stops.
func truncated(err error) error {
	var syntaxErr *json.SyntaxError
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		(errors.As(err, &syntaxErr) && syntaxErr.Error() == "unexpected end of JSON input") {
		return errors.New("unexpected end of JSON input")
	}
	return err
}

// ChannelValue returns the value that hands a workflow the channel name.
func ChannelValue(name string) *autoflowv1.Value {
	return &autoflowv1.Value{Kind: &autoflowv1.Value_ChannelValue{ChannelValue: &autoflowv1.ChannelValue{Name: name}}}
}

func parseValue(dec *json.Decoder) (*autoflowv1.Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case nil:
		return &autoflowv1.Value{Kind: &autoflowv1.Value_NoneValue{}}, nil
	case bool:
		return &autoflowv1.Value{Kind: &autoflowv1.Value_BoolValue{BoolValue: t}}, nil
	case string:
		return stringValue(t), nil
	case json.Number:
		return parseNumber(t)
	case json.Delim:
		if t == '[' {
			return parseList(dec)
		}
		return parseDict(dec)
	default:
		return nil, fmt.Errorf("unexpected JSON token %v", tok)
	}
}

func parseNumber(n json.Number) (*autoflowv1.Value, error) {
	if strings.ContainsAny(n.String(), ".eE") {
		f, err := n.Float64()
		if err != nil {
			return nil, fmt.Errorf("number %s is out of range", n)
		}
		return &autoflowv1.Value{Kind: &autoflowv1.Value_FloatValue{FloatValue: f}}, nil
	}
	i, err := n.Int64()
	if err != nil {
		return nil, fmt.Errorf("integer %s is out of range", n)
	}
	return &autoflowv1.Value{Kind: &autoflowv1.Value_IntegerValue{IntegerValue: i}}, nil
}

func parseList(dec *json.Decoder) (*autoflowv1.Value, error) {
	list := &autoflowv1.ListValue{}
	for dec.More() {
		v, err := parseValue(dec)
		if err != nil {
			return nil, err
		}
		list.Values = append(list.Values, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return &autoflowv1.Value{Kind: &autoflowv1.Value_ListValue{ListValue: list}}, nil
}

func parseDict(dec *json.Decoder) (*autoflowv1.Value, error) {
	dict := &autoflowv1.DictValue{}
	seen := map[string]struct{}{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		// The decoder only yields a string where an object key belongs.
		key, _ := tok.(string)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		seen[key] = struct{}{}
		v, err := parseValue(dec)
		if err != nil {
			return nil, err
		}
		dict.KeyValues = append(dict.KeyValues, &autoflowv1.KeyValue{Key: stringValue(key), Val: v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return &autoflowv1.Value{Kind: &autoflowv1.Value_DictValue{DictValue: dict}}, nil
}

func stringValue(s string) *autoflowv1.Value {
	return &autoflowv1.Value{Kind: &autoflowv1.Value_StringValue{StringValue: s}}
}
