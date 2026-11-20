package cmdutils

import (
	"fmt"
	"strconv"
)

// ParseID parses a string into a positive 64-bit integer ID.
// If the string cannot be parsed as an integer or the value is less than 1,
// an error naming both the rejected value and the argument's role is returned.
func ParseID(value, name string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid %s: %q", name, value)
	}

	return id, nil
}
