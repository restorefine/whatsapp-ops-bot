package whatsapp

import "strings"

// Split breaks text into chunks of at most max characters (runes). It prefers
// to cut at a blank line, then a newline, then a space, and only cuts inside a
// word when it has to.
func Split(text string, max int) []string {
	text = strings.TrimSpace(text)
	if max <= 0 {
		return []string{text}
	}
	var parts []string
	runes := []rune(text)
	for len(runes) > max {
		cut := bestCut(runes[:max+1])
		if cut <= 0 {
			cut = max
		}
		part := strings.TrimSpace(string(runes[:cut]))
		rest := strings.TrimLeft(string(runes[cut:]), " \n")
		// A block longer than max had to be cut: close it here, reopen it next.
		if strings.Count(part, "```")%2 != 0 {
			part += "\n```"
			rest = "```\n" + rest
		}
		if part != "" {
			parts = append(parts, part)
		}
		runes = []rune(rest)
	}
	if rest := strings.TrimSpace(string(runes)); rest != "" || len(parts) == 0 {
		parts = append(parts, rest)
	}
	return parts
}

// bestCut returns the index to cut window at, preferring paragraph, line and
// word boundaries in that order, and never inside a ``` monospace block. It
// ignores boundaries in the first quarter so chunks do not end up tiny.
func bestCut(window []rune) int {
	s := string(window)
	minCut := len(window) / 4
	for _, sep := range []string{"\n\n", "\n", " "} {
		for end := len(s); ; {
			i := strings.LastIndex(s[:end], sep)
			if i <= 0 {
				break
			}
			idx := len([]rune(s[:i]))
			if idx <= minCut {
				break
			}
			if strings.Count(s[:i], "```")%2 == 0 {
				return idx
			}
			end = i
		}
	}
	return -1
}
