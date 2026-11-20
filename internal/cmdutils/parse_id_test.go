package cmdutils

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     string
		argName   string
		wantID    int64
		wantErr   string
		shouldErr bool
	}{
		{
			name:      "valid positive integer",
			value:     "42",
			argName:   "runner ID",
			wantID:    42,
			shouldErr: false,
		},
		{
			name:      "valid large positive integer",
			value:     "9223372036854775807",
			argName:   "project ID",
			wantID:    math.MaxInt64,
			shouldErr: false,
		},
		{
			name:      "zero is rejected",
			value:     "0",
			argName:   "runner ID",
			wantErr:   `invalid runner ID: "0"`,
			shouldErr: true,
		},
		{
			name:      "negative integer is rejected",
			value:     "-5",
			argName:   "controller ID",
			wantErr:   `invalid controller ID: "-5"`,
			shouldErr: true,
		},
		{
			name:      "non-numeric string is rejected",
			value:     "abc",
			argName:   "runner controller ID",
			wantErr:   `invalid runner controller ID: "abc"`,
			shouldErr: true,
		},
		{
			name:      "empty string is rejected",
			value:     "",
			argName:   "token ID",
			wantErr:   `invalid token ID: ""`,
			shouldErr: true,
		},
		{
			name:      "float string is rejected",
			value:     "12.34",
			argName:   "repository ID",
			wantErr:   `invalid repository ID: "12.34"`,
			shouldErr: true,
		},
		{
			name:      "overflow is rejected",
			value:     "9223372036854775808",
			argName:   "job ID",
			wantErr:   `invalid job ID: "9223372036854775808"`,
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotID, err := ParseID(tt.value, tt.argName)
			if tt.shouldErr {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				assert.Zero(t, gotID)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantID, gotID)
			}
		})
	}
}
