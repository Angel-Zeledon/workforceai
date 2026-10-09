package drive

import "strings"

// Diff limits: the diff is computed on demand, only for text types.
const (
	maxDiffLines = 4000
	maxDiffCells = 4_000_000
	maxDiffOut   = 200_000
)

// DiffLine is one line of a diff: op is " " (same), "-" (removed) or "+" (added).
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// DiffResult compares two versions of a text file.
type DiffResult struct {
	From      int        `json:"from"`
	To        int        `json:"to"`
	Added     int        `json:"added"`
	Removed   int        `json:"removed"`
	Lines     []DiffLine `json:"lines"`
	Truncated bool       `json:"truncated"`
	// Unified is a unified-diff style text (context lines included).
	Unified string `json:"unified"`
}

// DiffText computes a line diff (common prefix/suffix trimmed, LCS in between).
// Inputs beyond the limits are reported as fully replaced hunks (Truncated).
func DiffText(a, b string) (lines []DiffLine, added, removed int, truncated bool) {
	al, bl := splitLines(a), splitLines(b)
	p := 0
	for p < len(al) && p < len(bl) && al[p] == bl[p] {
		p++
	}
	s := 0
	for s < len(al)-p && s < len(bl)-p && al[len(al)-1-s] == bl[len(bl)-1-s] {
		s++
	}
	am, bm := al[p:len(al)-s], bl[p:len(bl)-s]
	var mid []DiffLine
	if len(am) > maxDiffLines || len(bm) > maxDiffLines || len(am)*len(bm) > maxDiffCells {
		truncated = true
		for _, l := range am {
			mid = append(mid, DiffLine{"-", l})
		}
		for _, l := range bm {
			mid = append(mid, DiffLine{"+", l})
		}
	} else {
		mid = lcsDiff(am, bm)
	}
	for _, l := range al[:p] {
		lines = append(lines, DiffLine{" ", l})
	}
	lines = append(lines, mid...)
	for _, l := range al[len(al)-s:] {
		lines = append(lines, DiffLine{" ", l})
	}
	for _, l := range mid {
		switch l.Op {
		case "+":
			added++
		case "-":
			removed++
		}
	}
	return lines, added, removed, truncated
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func lcsDiff(a, b []string) []DiffLine {
	n, m := len(a), len(b)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var out []DiffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{" ", a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, DiffLine{"-", a[i]})
			i++
		default:
			out = append(out, DiffLine{"+", b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{"-", a[i]})
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{"+", b[j]})
	}
	return out
}

// unified renders changed lines with up to ctx lines of context.
func unified(lines []DiffLine, ctx int) (string, bool) {
	keep := make([]bool, len(lines))
	for i, l := range lines {
		if l.Op != " " {
			for k := max(0, i-ctx); k <= min(len(lines)-1, i+ctx); k++ {
				keep[k] = true
			}
		}
	}
	var b strings.Builder
	gap := false
	for i, l := range lines {
		if !keep[i] {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteString("@@\n")
		}
		gap = false
		b.WriteString(l.Op + l.Text + "\n")
		if b.Len() > maxDiffOut {
			return b.String(), true
		}
	}
	return b.String(), false
}
