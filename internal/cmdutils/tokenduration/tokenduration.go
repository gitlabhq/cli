package tokenduration

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var durationRegex = regexp.MustCompile(`^(\d+)([dhw])$`)

// TokenDuration wraps time.Duration to provide custom parsing for token lifetimes.
// It supports parsing duration strings with h (hours), d (days), and w (weeks) suffixes.
// This type implements the pflag.Value interface for use with Cobra commands.
type TokenDuration time.Duration

// ParseDuration parses a duration string with support for hours (h), days (d), and weeks (w).
// It accepts formats like: "24h", "30d", "2w", "720h".
// The duration must be a whole number of days and between 1 and 365 days.
func ParseDuration(s string) (TokenDuration, error) {
	var d time.Duration

	if matches := durationRegex.FindStringSubmatch(s); len(matches) == 3 {
		num, err := strconv.Atoi(matches[1])
		if err != nil {
			return 0, fmt.Errorf("invalid duration value %q: %w", s, err)
		}
		var unit time.Duration
		switch matches[2] {
		case "d":
			unit = 24 * time.Hour
		case "w":
			unit = 7 * 24 * time.Hour
		case "h":
			unit = time.Hour
		}
		if num > int(365*24*time.Hour/unit) {
			return 0, fmt.Errorf("duration must be between 1 and 365 days")
		}
		d = time.Duration(num) * unit
	} else {
		return 0, fmt.Errorf("invalid duration format: %s (expected formats: 24h, 30d, 2w)", s)
	}

	// Validate the parsed duration
	td := TokenDuration(d)
	if err := td.validate(); err != nil {
		return 0, err
	}

	return td, nil
}

// validate checks if the token duration is valid for token lifetimes.
// Returns an error if the duration is not a whole number of days or is outside the 1-365 day range.
func (d TokenDuration) validate() error {
	duration := time.Duration(d)

	// Ensure duration is a whole number of days
	if duration%(24*time.Hour) != 0 {
		return fmt.Errorf("duration must be in whole days (examples: 1d, 24h, 7d, 168h)")
	}

	// Validate duration is between 1 and 365 days
	if duration < 24*time.Hour || duration > 365*24*time.Hour {
		return fmt.Errorf("duration must be between 1 and 365 days")
	}

	return nil
}

// String returns the duration formatted as a string.
// The String() function is required for Cobra to display the default value.
func (d TokenDuration) String() string {
	duration := time.Duration(d)
	hours := duration.Hours()

	// Display in the most readable format
	if hours >= 24 && int(hours)%(24*7) == 0 {
		// Display as weeks if it's a whole number of weeks
		weeks := int(hours) / (24 * 7)
		return fmt.Sprintf("%dw", weeks)
	} else if hours >= 24 && int(hours)%24 == 0 {
		// Display as days if it's a whole number of days
		days := int(hours) / 24
		return fmt.Sprintf("%dd", days)
	}
	// Otherwise display as hours
	return duration.String()
}

// Set parses the given value and sets this TokenDuration.
// The Set() function is required for Cobra to parse command line arguments.
func (d *TokenDuration) Set(value string) error {
	parsed, err := ParseDuration(value)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Type returns the type name for help text.
// The Type() function is required for Cobra to display type information in help.
func (d *TokenDuration) Type() string {
	return "duration"
}

// Duration returns the underlying time.Duration value.
func (d TokenDuration) Duration() time.Duration {
	return time.Duration(d)
}

// CalculateExpirationDate calculates an expiration date by adding the duration to today's date.
// It uses date-only arithmetic (AddDate) since token expiration dates are dates without times.
// Returns a time.Time at midnight UTC representing the expiration date.
func (d TokenDuration) CalculateExpirationDate() time.Time {
	now := time.Now()
	days := int(d.Duration().Hours() / 24)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, days)
}
