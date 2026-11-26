package changelog

import (
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// testVersions returns the available version numbers, in ascending order,
// derived directly from the embedded files so the tests do not need to be
// updated when a new release is added.
func testVersions(t *testing.T) []int {
	t.Helper()

	names, err := fs.Glob(Files, "v*.md")
	require.NoError(t, err)
	require.NotEmpty(t, names)

	nums := make([]int, 0, len(names))
	for _, file := range names {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(file, "v"), ".md"))
		require.NoError(t, err, file)
		nums = append(nums, n)
	}
	sort.Ints(nums)
	return nums
}

func TestChangelog_Get(t *testing.T) {
	nums := testVersions(t)
	lo, hi := nums[0], nums[len(nums)-1]
	loName := "v" + strconv.Itoa(lo)

	// with or without the leading "v", with surrounding whitespace
	for _, version := range []string{loName, strconv.Itoa(lo), " " + strconv.Itoa(lo) + " ", " " + loName} {
		note, err := Get(version)
		require.NoError(t, err, version)
		require.True(t, strings.HasPrefix(note, "## "+loName+"\n"), version)
	}

	_, err := Get("v" + strconv.Itoa(hi+1))
	require.ErrorIs(t, err, fs.ErrNotExist)

	_, err = Get("abc")
	require.ErrorContains(t, err, "invalid version number")
}

func TestChangelog_Versions(t *testing.T) {
	nums := testVersions(t)

	versions := Versions()
	// every embedded file is listed
	require.Len(t, versions, len(nums))
	// oldest first, newest last, in ascending version number
	got := make([]int, len(versions))
	for i, v := range versions {
		n, err := number(v)
		require.NoError(t, err, v)
		got[i] = n
	}
	require.Equal(t, nums, got)

	for _, v := range versions {
		_, err := Get(v)
		require.NoError(t, err, v)
	}
}

func TestChangelog_Latest(t *testing.T) {
	nums := testVersions(t)

	latest, err := Latest()
	require.NoError(t, err)
	require.Equal(t, "v"+strconv.Itoa(nums[len(nums)-1]), latest)

	versions := Versions()
	require.Equal(t, versions[len(versions)-1], latest)

	_, err = Get(latest)
	require.NoError(t, err)
}
