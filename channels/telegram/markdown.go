package telegram

// Models write markdown; Telegram takes a small set of HTML tags. This
// turns the common markdown into those tags: headings and **bold** into
// <b>, *italic* into <i>, ~~strike~~ into <s>, `code` and fenced blocks
// into <code> and <pre>, [links](https://…) into <a>, and list dashes into
// bullets. Anything else stays as written, escaped.

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	codeSpan = regexp.MustCompile("`([^`\n]+)`")
	link     = regexp.MustCompile(`\[([^\]\n]+)\]\(((?:https?|tg)://[^)\s]+)\)`)
	bold     = regexp.MustCompile(`\*\*([^*\n]+)\*\*|__([^_\n]+)__`)
	italic   = regexp.MustCompile(`(^|[^\w*])\*([^*\s](?:[^*\n]*[^*\s])?)\*`)
	strike   = regexp.MustCompile(`~~([^~\n]+)~~`)
	heading  = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*$`)
	bullet   = regexp.MustCompile(`^(\s*)[-*+]\s+`)
)

func toHTML(md string) string {
	var out []string
	var block []string // lines of an open fenced block
	inBlock := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inBlock {
				out = append(out, "<pre>"+html.EscapeString(strings.Join(block, "\n"))+"</pre>")
				block = nil
			}
			inBlock = !inBlock
			continue
		}
		if inBlock {
			block = append(block, line)
			continue
		}
		out = append(out, inline(line))
	}
	if inBlock { // a block left open, as when a long message was split in it
		out = append(out, "<pre>"+html.EscapeString(strings.Join(block, "\n"))+"</pre>")
	}
	return strings.Join(out, "\n")
}

func inline(line string) string {
	if m := heading.FindStringSubmatch(line); m != nil {
		line = "**" + m[1] + "**"
	}
	line = bullet.ReplaceAllString(line, "$1• ")

	// Code spans and links are set aside first, so the markers inside them
	// stay as written.
	var kept []string
	keep := func(s string) string {
		kept = append(kept, s)
		return fmt.Sprintf("\x00%d\x00", len(kept)-1)
	}
	line = codeSpan.ReplaceAllStringFunc(line, func(s string) string {
		return keep("<code>" + html.EscapeString(codeSpan.FindStringSubmatch(s)[1]) + "</code>")
	})
	line = link.ReplaceAllStringFunc(line, func(s string) string {
		m := link.FindStringSubmatch(s)
		return keep(`<a href="` + html.EscapeString(m[2]) + `">` + html.EscapeString(m[1]) + "</a>")
	})

	line = html.EscapeString(line)
	line = bold.ReplaceAllStringFunc(line, func(s string) string {
		m := bold.FindStringSubmatch(s)
		return "<b>" + m[1] + m[2] + "</b>"
	})
	line = italic.ReplaceAllString(line, "$1<i>$2</i>")
	line = strike.ReplaceAllString(line, "<s>$1</s>")

	for i, s := range kept {
		line = strings.Replace(line, fmt.Sprintf("\x00%d\x00", i), s, 1)
	}
	return line
}
