package webtool

import (
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var (
	reHiddenStyle = regexp.MustCompile(`(?i)(display\s*:\s*none|visibility\s*:\s*hidden|font-size\s*:\s*0(px|pt|em|rem|%)?\s*(;|$)|opacity\s*:\s*0(\.0+)?\s*(;|$)|(^|;)\s*width\s*:\s*0|height\s*:\s*0(px)?\s*;?\s*overflow\s*:\s*hidden|text-indent\s*:\s*-\d{3,})`)
	reWS          = regexp.MustCompile(`[ \t\f\v\r\x{00a0}]+`)
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
	// zero-width and bidi control characters hide text from humans, not from the model
	reInvisible = regexp.MustCompile(`[\x{200b}-\x{200f}\x{202a}-\x{202e}\x{2060}-\x{2064}\x{feff}\x{00ad}]`)
)

var skipTags = map[atom.Atom]bool{atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true, atom.Head: true,
	atom.Iframe: true, atom.Object: true, atom.Embed: true, atom.Svg: true, atom.Canvas: true, atom.Math: true, atom.Select: true,
	atom.Textarea: true, atom.Button: true, atom.Audio: true, atom.Video: true, atom.Map: true, atom.Dialog: true}

var blockTags = map[atom.Atom]bool{atom.P: true, atom.Div: true, atom.Section: true, atom.Article: true, atom.Header: true, atom.Footer: true,
	atom.Main: true, atom.Aside: true, atom.Nav: true, atom.Ul: true, atom.Ol: true, atom.Blockquote: true, atom.Pre: true, atom.Br: true,
	atom.Hr: true, atom.Figure: true, atom.Figcaption: true, atom.Dl: true, atom.Dt: true, atom.Dd: true, atom.Address: true,
	atom.Form: true, atom.Fieldset: true, atom.Details: true, atom.Summary: true}

type extractor struct {
	b    strings.Builder
	base *url.URL
}

// ExtractHTML turns an HTML document into readable text: no scripts, styles, comments or hidden elements;
// headings as "# title", lists as "- item", links as "text (url)", table rows on one line. Nothing is executed
// and nothing is fetched.
func ExtractHTML(src string, base *url.URL) (title, text string) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return "", cleanText(src)
	}
	title = findTitle(doc)
	e := &extractor{base: base}
	e.walk(doc)
	return title, cleanText(e.b.String())
}

func findTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.DataAtom == atom.Title {
		return cleanInline(textOf(n))
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := findTitle(c); t != "" {
			return t
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && skipTags[c.DataAtom] {
				continue
			}
			rec(c)
		}
	}
	rec(n)
	return sb.String()
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func hidden(n *html.Node) bool {
	for _, a := range n.Attr {
		switch a.Key {
		case "hidden":
			return true
		case "aria-hidden":
			if strings.EqualFold(strings.TrimSpace(a.Val), "true") {
				return true
			}
		case "style":
			if reHiddenStyle.MatchString(a.Val) {
				return true
			}
		case "type":
			if n.DataAtom == atom.Input && strings.EqualFold(a.Val, "hidden") {
				return true
			}
		}
	}
	return false
}

func cleanInline(s string) string {
	s = reInvisible.ReplaceAllString(s, "")
	return strings.TrimSpace(reWS.ReplaceAllString(strings.Join(strings.Fields(s), " "), " "))
}

func cleanText(s string) string {
	s = reInvisible.ReplaceAllString(s, "")
	lines := strings.Split(strings.ReplaceAll(s, "\r", ""), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(reWS.ReplaceAllString(l, " "))
	}
	return strings.TrimSpace(reBlankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

func (e *extractor) nl() {
	s := e.b.String()
	if s != "" && !strings.HasSuffix(s, "\n") {
		e.b.WriteByte('\n')
	}
}

func (e *extractor) walk(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		e.b.WriteString(n.Data)
		return
	case html.ElementNode:
		if skipTags[n.DataAtom] || hidden(n) {
			return
		}
		e.element(n)
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling { // document, doctype, comment (ignored)
		if c.Type == html.CommentNode {
			continue
		}
		e.walk(c)
	}
}

func (e *extractor) children(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		e.walk(c)
	}
}

func (e *extractor) element(n *html.Node) {
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		e.nl()
		e.b.WriteString("\n" + strings.Repeat("#", int(n.Data[1]-'0')) + " " + cleanInline(textOf(n)) + "\n")
	case atom.A:
		label := cleanInline(textOf(n))
		href := e.href(attr(n, "href"))
		switch {
		case href == "":
			e.b.WriteString(label)
		case label == "" || label == href:
			e.b.WriteString(href)
		default:
			e.b.WriteString(label + " (" + href + ")")
		}
	case atom.Img:
		if alt := cleanInline(attr(n, "alt")); alt != "" {
			e.b.WriteString("[" + alt + "]")
		}
	case atom.Li:
		e.nl()
		e.b.WriteString("- ")
		e.children(n)
		e.nl()
	case atom.Table:
		e.nl()
		e.children(n)
		e.nl()
	case atom.Tr:
		e.nl()
		var cells []string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			cells = append(cells, e.rowCells(c)...)
		}
		e.b.WriteString(strings.Join(cells, " | "))
		e.nl()
	case atom.Thead, atom.Tbody, atom.Tfoot, atom.Caption:
		e.nl()
		e.children(n)
		e.nl()
	default:
		if blockTags[n.DataAtom] {
			e.nl()
			e.children(n)
			e.nl()
			return
		}
		e.children(n)
	}
}

func (e *extractor) rowCells(c *html.Node) []string {
	if c.Type != html.ElementNode {
		return nil
	}
	if c.DataAtom != atom.Td && c.DataAtom != atom.Th {
		return nil
	}
	if hidden(c) {
		return nil
	}
	sub := &extractor{base: e.base}
	sub.children(c)
	if t := cleanInline(strings.ReplaceAll(sub.b.String(), "\n", " ")); t != "" {
		return []string{t}
	}
	return nil
}

// href resolves a link against the page and keeps only http(s) links without credentials.
func (e *extractor) href(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if e.base != nil {
		u = e.base.ResolveReference(u)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	u.Fragment = ""
	s := u.String()
	if len(s) > 300 {
		return ""
	}
	return s
}
