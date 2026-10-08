package commands

import (
	"regexp"
	"strings"
)

var (
	mdEscape  = regexp.MustCompile(`\\([\\_*\[\]()#~` + "`" + `>!|.-])`)
	mdLink    = regexp.MustCompile(`\[([^\]]*)\]\((https?://[^)\s]+)\)`)
	mdHeading = regexp.MustCompile(`(?m)^#{1,6}\s*(.+?)\s*$`)
	mdBold    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdBlank   = regexp.MustCompile(`\n{3,}`)
)

// whatsappText turns a ClickUp markdown description into WhatsApp text.
// ClickUp stores a pasted Drive link as a "link card" whose label repeats the
// domain and the URL; it becomes the bare, clickable URL. Headings and bold
// become WhatsApp bold.
func whatsappText(md string) string {
	s := strings.ReplaceAll(md, "\r\n", "\n")
	s = mdEscape.ReplaceAllString(s, "$1")
	s = mdLink.ReplaceAllStringFunc(s, func(m string) string {
		parts := mdLink.FindStringSubmatch(m)
		label := strings.Join(strings.Fields(parts[1]), " ")
		url := parts[2]
		if label == "" || strings.Contains(label, "http") || strings.Contains(url, label) || !strings.Contains(label, " ") && strings.Contains(label, ".") {
			return url
		}
		return label + ": " + url
	})
	s = mdHeading.ReplaceAllString(s, "*$1*")
	s = mdBold.ReplaceAllString(s, "*$1*")
	s = strings.ReplaceAll(s, "***", "*")
	s = mdBlank.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
