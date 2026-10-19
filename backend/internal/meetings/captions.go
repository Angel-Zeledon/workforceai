package meetings

import (
	"regexp"
	"strings"
)

var (
	reVoice   = regexp.MustCompile(`<v(?:\.[\w.]+)?\s+([^>]+)>`)
	reTag     = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	reTimeRow = regexp.MustCompile(`-->`)
)

// CleanCaptions turns a WebVTT or SRT file (what Meet, Zoom and Teams export as a transcript)
// into plain "Speaker: text" lines: the header, cue numbers, timings, NOTE blocks and markup
// are dropped. Text that is not a caption file is returned unchanged.
func CleanCaptions(text string) string {
	t := strings.ReplaceAll(strings.TrimPrefix(text, "\xef\xbb\xbf"), "\r\n", "\n")
	if !strings.HasPrefix(strings.TrimSpace(t), "WEBVTT") && !reTimeRow.MatchString(t) {
		return text
	}
	lines := strings.Split(t, "\n")
	var out []string
	skipBlock := false
	for i := 0; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		switch {
		case l == "":
			skipBlock = false
			continue
		case skipBlock, strings.HasPrefix(l, "WEBVTT"), reTimeRow.MatchString(l):
			continue
		case strings.HasPrefix(l, "NOTE") || strings.HasPrefix(l, "STYLE") || strings.HasPrefix(l, "REGION"):
			skipBlock = true
			continue
		}
		// a cue number or id on the line right before a timing line
		if i+1 < len(lines) && reTimeRow.MatchString(lines[i+1]) {
			continue
		}
		l = reVoice.ReplaceAllString(l, "$1: ")
		l = strings.TrimSpace(reTag.ReplaceAllString(l, ""))
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
