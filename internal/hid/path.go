package hid

import (
	"regexp"
	"strconv"
)

var hidVIDPID = regexp.MustCompile(`(?i)(?:vid_([0-9a-f]{4})&pid_([0-9a-f]{4})|vid&[0-9a-f]{4}([0-9a-f]{4})_pid&([0-9a-f]{4}))`)

func parsePath(path string) (vid, pid uint16, ok bool) {
	m := hidVIDPID.FindStringSubmatch(path)
	if m == nil {
		return 0, 0, false
	}
	vi, pi := m[1], m[2]
	if vi == "" {
		vi, pi = m[3], m[4]
	}
	v, e1 := strconv.ParseUint(vi, 16, 16)
	p, e2 := strconv.ParseUint(pi, 16, 16)
	if e1 != nil || e2 != nil {
		return 0, 0, false
	}
	return uint16(v), uint16(p), true
}
