//go:build !integration

package autoflow

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	autoflowv1 "gitlab.com/gitlab-org/protos/gen/go/gitlab/autoflow/v1"
)

func str(s string) *autoflowv1.Value {
	return &autoflowv1.Value{Kind: &autoflowv1.Value_StringValue{StringValue: s}}
}

func num(i int64) *autoflowv1.Value {
	return &autoflowv1.Value{Kind: &autoflowv1.Value_IntegerValue{IntegerValue: i}}
}

func float(f float64) *autoflowv1.Value {
	return &autoflowv1.Value{Kind: &autoflowv1.Value_FloatValue{FloatValue: f}}
}

func list(vs ...*autoflowv1.Value) *autoflowv1.Value {
	return &autoflowv1.Value{Kind: &autoflowv1.Value_ListValue{ListValue: &autoflowv1.ListValue{Values: vs}}}
}

func TestMarshalValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value *autoflowv1.Value
		want  string
	}{
		{name: "none", value: &autoflowv1.Value{Kind: &autoflowv1.Value_NoneValue{}}, want: `null`},
		{name: "bool", value: &autoflowv1.Value{Kind: &autoflowv1.Value_BoolValue{BoolValue: false}}, want: `false`},
		{name: "integer", value: num(math.MaxInt64), want: `9223372036854775807`},
		{name: "float with fraction", value: float(1.5), want: `1.5`},
		{name: "whole float", value: float(2), want: `2.0`},
		{name: "large float", value: float(1234567), want: `1234567.0`},
		{name: "small float", value: float(0.0001), want: `0.0001`},
		{name: "float with exponent", value: float(1e21), want: `1e+21`},
		{name: "tiny float", value: float(1e-5), want: `1e-05`},
		{name: "missing list element", value: list(nil, &autoflowv1.Value{}), want: `[null,null]`},
		{name: "string", value: str(`<a "b">`), want: `"<a \"b\">"`},
		{name: "list", value: list(num(1), list()), want: `[1,[]]`},
		{
			name: "dict with string keys",
			value: &autoflowv1.Value{Kind: &autoflowv1.Value_DictValue{DictValue: &autoflowv1.DictValue{KeyValues: []*autoflowv1.KeyValue{
				{Key: str("b"), Val: num(1)},
				{Key: str("a"), Val: str("x")},
			}}}},
			want: `{"b":1,"a":"x"}`,
		},
		{
			name: "dict with an integer key",
			value: &autoflowv1.Value{Kind: &autoflowv1.Value_DictValue{DictValue: &autoflowv1.DictValue{KeyValues: []*autoflowv1.KeyValue{
				{Key: num(1), Val: num(2)},
			}}}},
			want: `{"dict_value":{"key_values":[{"key":{"integer_value":"1"},"val":{"integer_value":"2"}}]}}`,
		},
		{
			name:  "channel inside a list",
			value: list(&autoflowv1.Value{Kind: &autoflowv1.Value_ChannelValue{ChannelValue: &autoflowv1.ChannelValue{Name: "inbox"}}}),
			want:  `[{"channel_value":{"name":"inbox"}}]`,
		},
		{name: "infinity", value: float(math.Inf(1)), want: `{"float_value":"Infinity"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := MarshalValue(tc.value)
			require.NoError(t, err)
			// Compared byte for byte: the output is compact and keeps the dict
			// order, which JSONEq would not check.
			assert.Equal(t, []byte(tc.want), got)
		})
	}
}

func TestMarshalProtoValue(t *testing.T) {
	t.Parallel()

	got, err := MarshalProtoValue(list(num(1)))
	require.NoError(t, err)
	assert.JSONEq(t, `{"list_value":{"values":[{"integer_value":"1"}]}}`, string(got))
}
