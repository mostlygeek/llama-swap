package changelog

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChangelog_Get(t *testing.T) {
	// with or without the leading "v", with surrounding whitespace
	for _, version := range []string{"v200", "200", " 200 ", " v200"} {
		note, err := Get(version)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(note, "## v200\n"), version)
	}

	_, err := Get("v99999")
	require.ErrorIs(t, err, fs.ErrNotExist)

	_, err = Get("abc")
	require.ErrorContains(t, err, "invalid version number")
}

func TestChangelog_Versions(t *testing.T) {
	names, err := fs.Glob(Files, "v*.md")
	require.NoError(t, err)

	versions := Versions()
	// every embedded file is listed
	require.Len(t, versions, len(names))
	// oldest first, newest last, in ascending version number
	require.Equal(t, "v200", versions[0])
	require.Equal(t, "v272", versions[len(versions)-1])
	for i := 1; i < len(versions); i++ {
		prev, err := strconv.Atoi(versions[i-1][1:])
		require.NoError(t, err)
		cur, err := strconv.Atoi(versions[i][1:])
		require.NoError(t, err)
		require.Greater(t, cur, prev)
	}

	for _, v := range versions {
		_, err := Get(v)
		require.NoError(t, err, v)
	}
}

func TestChangelog_Latest(t *testing.T) {
	latest, err := Latest()
	require.NoError(t, err)

	versions := Versions()
	require.Equal(t, versions[len(versions)-1], latest)

	_, err = Get(latest)
	require.NoError(t, err)
}
