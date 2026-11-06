//go:build !integration

package autoflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	autoflowv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/v1"
	"google.golang.org/protobuf/proto"
)

func integer(i int64) *autoflowv1.Value {
	return &autoflowv1.Value{Kind: &autoflowv1.Value_IntegerValue{IntegerValue: i}}
}

func TestParseValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		json string
		want *autoflowv1.Value
	}{
		{json: `null`, want: &autoflowv1.Value{Kind: &autoflowv1.Value_NoneValue{}}},
		{json: `true`, want: &autoflowv1.Value{Kind: &autoflowv1.Value_BoolValue{BoolValue: true}}},
		{json: `"hi"`, want: stringValue("hi")},
		{json: `-42`, want: integer(-42)},
		{json: `9223372036854775807`, want: integer(9223372036854775807)},
		{json: `1.5`, want: &autoflowv1.Value{Kind: &autoflowv1.Value_FloatValue{FloatValue: 1.5}}},
		{json: `1e3`, want: &autoflowv1.Value{Kind: &autoflowv1.Value_FloatValue{FloatValue: 1000}}},
		{json: ` [1, [2]] `, want: &autoflowv1.Value{Kind: &autoflowv1.Value_ListValue{ListValue: &autoflowv1.ListValue{Values: []*autoflowv1.Value{
			integer(1),
			{Kind: &autoflowv1.Value_ListValue{ListValue: &autoflowv1.ListValue{Values: []*autoflowv1.Value{integer(2)}}}},
		}}}}},
		{json: `[]`, want: &autoflowv1.Value{Kind: &autoflowv1.Value_ListValue{ListValue: &autoflowv1.ListValue{}}}},
		{json: `{"b": 1, "a": {}}`, want: &autoflowv1.Value{Kind: &autoflowv1.Value_DictValue{DictValue: &autoflowv1.DictValue{KeyValues: []*autoflowv1.KeyValue{
			{Key: stringValue("b"), Val: integer(1)},
			{Key: stringValue("a"), Val: &autoflowv1.Value{Kind: &autoflowv1.Value_DictValue{DictValue: &autoflowv1.DictValue{}}}},
		}}}}},
	}
	for _, tc := range tests {
		t.Run(tc.json, func(t *testing.T) {
			t.Parallel()

			got, err := ParseValue([]byte(tc.json))
			require.NoError(t, err)
			assert.True(t, proto.Equal(tc.want, got), "got %v", got)
		})
	}
}

func TestParseValue_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		json    string
		wantErr string
	}{
		{json: ``, wantErr: "no JSON value"},
		{json: `1 2`, wantErr: "more than one JSON value"},
		{json: `9223372036854775808`, wantErr: "integer 9223372036854775808 is out of range"},
		{json: `1e999`, wantErr: "number 1e999 is out of range"},
		{json: `{"a": 1, "a": 2}`, wantErr: `duplicate key "a"`},
		{json: `[1,`, wantErr: "unexpected end of JSON input"},
		{json: `[1`, wantErr: "unexpected end of JSON input"},
		{json: `{"a"`, wantErr: "unexpected end of JSON input"},
		{json: `"abc`, wantErr: "unexpected end of JSON input"},
		{json: `[1]]`, wantErr: "invalid character ']'"},
		{json: " ", wantErr: "no JSON value"},
		{json: `hi`, wantErr: "invalid character"},
	}
	for _, tc := range tests {
		t.Run(tc.json, func(t *testing.T) {
			t.Parallel()

			_, err := ParseValue([]byte(tc.json))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestParseValue_RoundTrip(t *testing.T) {
	t.Parallel()

	for _, doc := range []string{`null`, `true`, `-7`, `2.0`, `1.5e-07`, `"a<b"`, `[1,[null,{}]]`, `{"z":1,"a":[2.5,"x"]}`} {
		t.Run(doc, func(t *testing.T) {
			t.Parallel()

			v, err := ParseValue([]byte(doc))
			require.NoError(t, err)
			got, err := MarshalValue(v)
			require.NoError(t, err)
			assert.Equal(t, doc, string(got))
		})
	}
}
