package spl

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// gjsonPath renders a body path for gjson/sjson. Negative indexes are resolved
// against the current body: the array at the prefix is read and len+index is
// substituted. ok is false when a negative index cannot be resolved (the
// prefix is not an array, or the index is out of range), in which case reads
// are absent and writes are no-ops.
func (p *path) gjsonPath(body []byte) (string, bool) {
	var sb strings.Builder
	for i, seg := range p.segs {
		prefix := sb.String()
		if i > 0 {
			sb.WriteByte('.')
		}
		if !seg.isIndex {
			sb.WriteString(seg.key)
			continue
		}
		idx := seg.index
		if idx < 0 {
			var arr gjson.Result
			if i == 0 {
				arr = gjson.ParseBytes(body)
			} else {
				arr = gjson.GetBytes(body, prefix)
			}
			if !arr.IsArray() {
				return "", false
			}
			idx += len(arr.Array())
			if idx < 0 {
				return "", false
			}
		}
		sb.WriteString(strconv.Itoa(idx))
	}
	return sb.String(), true
}

// key returns the single context key for a context path.
func (p *path) key() string {
	return p.segs[0].key
}

// firstKey returns the first body key of a body path, or "" when the path
// starts with an index.
func (p *path) firstKey() string {
	if p.ns != nsBody || len(p.segs) == 0 || p.segs[0].isIndex {
		return ""
	}
	return p.segs[0].key
}
