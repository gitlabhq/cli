package loader

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoaderNext_LoadsAllPages(t *testing.T) {
	pages := map[int][]int{
		1: {1, 2},
		2: {3, 4},
		3: {5},
	}

	nextPages := map[int]int{
		1: 2,
		2: 3,
		3: 0,
	}

	l := New(func(page int) ([]int, int, error) {
		return pages[page], nextPages[page], nil
	})

	var got [][]int

	for l.Next() {
		page := append([]int(nil), l.Page()...)
		got = append(got, page)
	}

	want := [][]int{
		{1, 2},
		{3, 4},
		{5},
	}

	require.Equal(t, want, got)
	require.NoError(t, l.Err())
}

func TestLoaderNext_EmptyResult(t *testing.T) {
	calls := 0

	l := New(func(page int) ([]int, int, error) {
		calls++
		return []int{}, 0, nil
	})

	require.True(t, l.Next(), "expected Next to return true on first call")
	assert.Empty(t, l.Page(), "expected empty page")

	require.False(t, l.Next(), "expected Next to return false after empty last page")
	require.NoError(t, l.Err())

	assert.Equal(t, 1, calls, "expected only 1 fetch")
}

func TestLoaderNext_MidStreamError(t *testing.T) {
	fetchErr := errors.New("server error")

	l := New(func(page int) ([]int, int, error) {
		switch page {
		case 1:
			return []int{1, 2}, 2, nil
		case 2:
			return nil, 0, fetchErr
		default:
			t.Fatalf("unexpected fetch for page %d", page)
			return nil, 0, nil
		}
	})

	// page 1 succeeds
	require.True(t, l.Next(), "expected Next to return true on page 1")
	assert.Equal(t, []int{1, 2}, l.Page())

	// page 2 errors — Next returns false
	require.False(t, l.Next(), "expected Next to return false on error")
	assert.Equal(t, fetchErr, l.Err())
}
