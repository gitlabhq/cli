//go:build !integration

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDateFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		wantAbsolute string
		wantRelative *RelativeDate
		wantErr      string
	}{
		{name: "date", input: "2026-08-01", wantAbsolute: "2026-08-01"},
		{name: "RFC 3339 timestamp", input: "2026-08-01T10:30:00+02:00", wantAbsolute: "2026-08-01T10:30:00+02:00"},
		{name: "days in the past", input: "-7d", wantRelative: &RelativeDate{Amount: -7, Unit: RelativeDateUnitDay}},
		{name: "weeks in the future", input: "2w", wantRelative: &RelativeDate{Amount: 2, Unit: RelativeDateUnitWeek}},
		{name: "explicit plus sign", input: "+3m", wantRelative: &RelativeDate{Amount: 3, Unit: RelativeDateUnitMonth}},
		{name: "years", input: "-1y", wantRelative: &RelativeDate{Amount: -1, Unit: RelativeDateUnitYear}},
		{name: "today", input: "0d", wantRelative: &RelativeDate{Amount: 0, Unit: RelativeDateUnitDay}},
		{name: "largest amount", input: "-9999d", wantRelative: &RelativeDate{Amount: -9999, Unit: RelativeDateUnitDay}},
		{name: "amount out of range", input: "10000d", wantErr: "between -9999 and 9999"},
		{name: "unknown unit", input: "-7h", wantErr: "relative date such as"},
		{name: "missing unit", input: "-7", wantErr: "relative date such as"},
		{name: "uppercase unit", input: "7D", wantErr: "relative date such as"},
		{name: "invalid date", input: "2026-13-01", wantErr: "YYYY-MM-DD"},
		{name: "empty", input: "", wantErr: "YYYY-MM-DD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDateFilter(tt.input)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantRelative, got.Relative)
			assert.Equal(t, tt.wantAbsolute, got.Absolute)
		})
	}
}

func TestAddDateFilterVariables(t *testing.T) {
	t.Parallel()

	t.Run("no date filters", func(t *testing.T) {
		t.Parallel()

		variables := map[string]any{}
		decls, args := addDateFilterVariables(variables, nil)

		assert.Empty(t, decls)
		assert.Empty(t, args)
		assert.Empty(t, variables)
	})

	t.Run("absolute and relative date filters", func(t *testing.T) {
		t.Parallel()

		variables := map[string]any{}
		decls, args := addDateFilterVariables(variables, map[DateField]DateFilter{
			UpdatedAfter:  {Absolute: "2026-01-01T10:00:00+02:00"},
			CreatedAfter:  {Relative: &RelativeDate{Amount: -7, Unit: RelativeDateUnitDay}},
			CreatedBefore: {Absolute: "2026-01-01"},
			DueBefore:     {Relative: &RelativeDate{Amount: 2, Unit: RelativeDateUnitWeek}},
		})

		assert.Equal(t, ", $createdBefore: Time, $updatedAfter: Time, $relativeDateFilters: WorkItemRelativeDateFiltersInput", decls)
		assert.Equal(t, ", createdBefore: $createdBefore, updatedAfter: $updatedAfter, relativeDateFilters: $relativeDateFilters", args)
		assert.Equal(t, map[string]any{
			"createdBefore": "2026-01-01",
			"updatedAfter":  "2026-01-01T10:00:00+02:00",
			"relativeDateFilters": map[DateField]RelativeDate{
				CreatedAfter: {Amount: -7, Unit: RelativeDateUnitDay},
				DueBefore:    {Amount: 2, Unit: RelativeDateUnitWeek},
			},
		}, variables)
	})
}
