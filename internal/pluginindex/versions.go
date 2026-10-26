package pluginindex

import (
	"strconv"
	"strings"
)

// parseVersion reads major.minor.patch, ignoring a pre-release and build;
// a missing or x part is -1.
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, s := range parts {
		if s == "x" || s == "X" || s == "*" {
			out[i] = -1
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	for i := len(parts); i < 3; i++ {
		out[i] = -1
	}
	return out, true
}

// Valid reports whether v is a version such as 1.2.3.
func Valid(v string) bool {
	p, ok := parseVersion(v)
	return ok && p[0] >= 0 && p[1] >= 0 && p[2] >= 0
}

// Compare compares two versions, as -1, 0 or 1.
func Compare(a, b string) int {
	va, _ := parseVersion(a)
	vb, _ := parseVersion(b)
	for i := range 3 {
		x, y := max(va[i], 0), max(vb[i], 0)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Satisfies reports whether version is in an npm-style range: comparators
// (>=, >, <=, <, =), ^ and ~ ranges, x wildcards, a - b, joined by spaces
// (and) and || (or). A version it cannot read satisfies every range, as a
// development build does.
func Satisfies(version, rng string) bool {
	v, ok := parseVersion(version)
	if !ok {
		return true
	}
	for _, alt := range strings.Split(rng, "||") {
		alt = strings.TrimSpace(alt)
		if alt == "" || alt == "*" {
			return true
		}
		if a, b, ok := strings.Cut(alt, " - "); ok {
			if cmpV(v, a) >= 0 && cmpV(v, b) <= 0 {
				return true
			}
			continue
		}
		all := true
		for _, c := range strings.Fields(alt) {
			if !satisfiesOne(v, c) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func cmpV(v [3]int, s string) int {
	w, _ := parseVersion(s)
	for i := range 3 {
		if w[i] < 0 {
			return 0
		}
		if v[i] != w[i] {
			if v[i] < w[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func satisfiesOne(v [3]int, c string) bool {
	for _, op := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		rest, ok := strings.CutPrefix(c, op)
		if !ok {
			continue
		}
		w, ok := parseVersion(rest)
		if !ok {
			return false
		}
		switch op {
		case ">=":
			return cmpV(v, rest) >= 0
		case "<=":
			return cmpV(v, rest) <= 0
		case ">":
			return cmpV(v, rest) > 0
		case "<":
			return cmpV(v, rest) < 0
		case "=":
			return cmpV(v, rest) == 0
		case "^":
			// ^0.8.1 is >=0.8.1 <0.9.0; ^1.2.3 is >=1.2.3 <2.0.0.
			if cmpV(v, rest) < 0 {
				return false
			}
			if w[0] > 0 || w[1] < 0 {
				return v[0] == w[0]
			}
			if w[1] > 0 || w[2] < 0 {
				return v[0] == 0 && v[1] == w[1]
			}
			return v[0] == 0 && v[1] == 0 && v[2] == w[2]
		case "~":
			if cmpV(v, rest) < 0 {
				return false
			}
			if w[1] < 0 {
				return v[0] == w[0]
			}
			return v[0] == w[0] && v[1] == w[1]
		}
	}
	return cmpV(v, c) == 0
}
