//go:build !integration

package tokenduration

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{
			name:    "1 day with d suffix",
			input:   "1d",
			want:    24 * time.Hour,
			wantErr: false,
		},
		{
			name:    "7 days with d suffix",
			input:   "7d",
			want:    7 * 24 * time.Hour,
			wantErr: false,
		},
		{
			name:    "30 days with d suffix",
			input:   "30d",
			want:    30 * 24 * time.Hour,
			wantErr: false,
		},
		{
			name:    "365 days with d suffix",
			input:   "365d",
			want:    365 * 24 * time.Hour,
			wantErr: false,
		},
		{
			name:    "1 week with w suffix",
			input:   "1w",
			want:    7 * 24 * time.Hour,
			wantErr: false,
		},
		{
			name:    "2 weeks with w suffix",
			input:   "2w",
			want:    14 * 24 * time.Hour,
			wantErr: false,
		},
		{
			name:    "52 weeks with w suffix",
			input:   "52w",
			want:    52 * 7 * 24 * time.Hour,
			wantErr: false,
		},
		{
			name:    "24 hours with h suffix (backward compat)",
			input:   "24h",
			want:    24 * time.Hour,
			wantErr: false,
		},
		{
			name:    "168 hours with h suffix (1 week)",
			input:   "168h",
			want:    168 * time.Hour,
			wantErr: false,
		},
		{
			name:    "720 hours with h suffix (30 days)",
			input:   "720h",
			want:    720 * time.Hour,
			wantErr: false,
		},
		{
			name:    "invalid: just a number",
			input:   "30",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid: fractional days",
			input:   "1.5d",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid: unsupported unit",
			input:   "1y",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid: month suffix",
			input:   "1M",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid: empty string",
			input:   "",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid: negative duration",
			input:   "-1d",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid: not multiple of 24h",
			input:   "33h",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid: 0 days",
			input:   "0d",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid: over 365 days",
			input:   "400d",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid: value exceeds 365-day limit",
			input:   "281474976710657d",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid: 366 days",
			input:   "366d",
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDuration(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && time.Duration(got) != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, time.Duration(got), tt.want)
			}
		})
	}
}

func TestTokenDuration_String(t *testing.T) {
	tests := []struct {
		name     string
		duration TokenDuration
		want     string
	}{
		{
			name:     "1 day",
			duration: TokenDuration(24 * time.Hour),
			want:     "1d",
		},
		{
			name:     "7 days (1 week)",
			duration: TokenDuration(7 * 24 * time.Hour),
			want:     "1w",
		},
		{
			name:     "14 days (2 weeks)",
			duration: TokenDuration(14 * 24 * time.Hour),
			want:     "2w",
		},
		{
			name:     "30 days",
			duration: TokenDuration(30 * 24 * time.Hour),
			want:     "30d",
		},
		{
			name:     "365 days (52 weeks + 1 day, should display as days)",
			duration: TokenDuration(365 * 24 * time.Hour),
			want:     "365d",
		},
		{
			name:     "12 hours",
			duration: TokenDuration(12 * time.Hour),
			want:     "12h0m0s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.duration.String()
			if got != tt.want {
				t.Errorf("TokenDuration.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTokenDuration_Set(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    TokenDuration
		wantErr bool
	}{
		{
			name:    "set 30d",
			value:   "30d",
			want:    TokenDuration(30 * 24 * time.Hour),
			wantErr: false,
		},
		{
			name:    "set 2w",
			value:   "2w",
			want:    TokenDuration(14 * 24 * time.Hour),
			wantErr: false,
		},
		{
			name:    "set 24h",
			value:   "24h",
			want:    TokenDuration(24 * time.Hour),
			wantErr: false,
		},
		{
			name:    "set invalid",
			value:   "invalid",
			want:    TokenDuration(0),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d TokenDuration
			err := d.Set(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("TokenDuration.Set(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
				return
			}
			if !tt.wantErr && d != tt.want {
				t.Errorf("TokenDuration.Set(%q) = %v, want %v", tt.value, d, tt.want)
			}
		})
	}
}

func TestTokenDuration_Type(t *testing.T) {
	var d TokenDuration
	if got := d.Type(); got != "duration" {
		t.Errorf("TokenDuration.Type() = %v, want %v", got, "duration")
	}
}

func TestTokenDuration_Duration(t *testing.T) {
	td := TokenDuration(30 * 24 * time.Hour)
	want := 30 * 24 * time.Hour
	if got := td.Duration(); got != want {
		t.Errorf("TokenDuration.Duration() = %v, want %v", got, want)
	}
}

func TestTokenDuration_CalculateExpirationDate(t *testing.T) {
	tests := []struct {
		name     string
		duration TokenDuration
		wantDays int // Number of days from today
	}{
		{
			name:     "1 day",
			duration: TokenDuration(24 * time.Hour),
			wantDays: 1,
		},
		{
			name:     "7 days",
			duration: TokenDuration(7 * 24 * time.Hour),
			wantDays: 7,
		},
		{
			name:     "30 days",
			duration: TokenDuration(30 * 24 * time.Hour),
			wantDays: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.duration.CalculateExpirationDate()

			// Verify it's at midnight UTC
			if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
				t.Errorf("CalculateExpirationDate() not at midnight UTC: %v", got)
			}

			// Verify it's in UTC
			if got.Location() != time.UTC {
				t.Errorf("CalculateExpirationDate() not in UTC: %v", got.Location())
			}

			// Verify it's the correct number of days from today
			now := time.Now()
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			expected := today.AddDate(0, 0, tt.wantDays)

			if !got.Equal(expected) {
				t.Errorf("CalculateExpirationDate() = %v, want %v", got, expected)
			}
		})
	}
}
