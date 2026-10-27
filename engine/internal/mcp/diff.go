package mcp

import "strings"

// lineDiff renders a compact unified-style diff (2 lines of context) of two YAML documents.
// Documents are small, so a quadratic LCS on the part between the common prefix and suffix is fine.
func lineDiff(before, after string) string {
	a, b := strings.Split(before, "\n"), strings.Split(after, "\n")
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	var mid []string
	if len(ma)*len(mb) > 4_000_000 {
		for _, l := range ma {
			mid = append(mid, "-"+l)
		}
		for _, l := range mb {
			mid = append(mid, "+"+l)
		}
	} else {
		mid = lcsDiff(ma, mb)
	}
	const ctx = 2
	var out []string
	from := max(0, pre-ctx)
	if from > 0 {
		out = append(out, "…")
	}
	for _, l := range a[from:pre] {
		out = append(out, " "+l)
	}
	out = append(out, mid...)
	to := min(len(a), len(a)-suf+ctx)
	for _, l := range a[len(a)-suf : to] {
		out = append(out, " "+l)
	}
	if to < len(a) {
		out = append(out, "…")
	}
	return strings.Join(out, "\n")
}

func lcsDiff(a, b []string) []string {
	n, m := len(a), len(b)
	t := make([][]int, n+1)
	for i := range t {
		t[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, " "+a[i])
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			out = append(out, "-"+a[i])
			i++
		default:
			out = append(out, "+"+b[j])
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, "-"+a[i])
	}
	for ; j < m; j++ {
		out = append(out, "+"+b[j])
	}
	return out
}
