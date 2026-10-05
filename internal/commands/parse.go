// Package commands parses WhatsApp commands and builds replies from ClickUp data.
package commands

import (
	"strings"
	"unicode"
)

// Command is a parsed message: "/Alice Smith!" becomes {Name: "alice", Args: "Smith"}.
type Command struct {
	Name string // lower case, without the slash
	Args string // original case, trimmed
}

// Parse normalises a message: trims whitespace, ignores trailing punctuation,
// makes the slash optional and lower-cases the command word.
func Parse(text string) Command {
	text = strings.TrimSpace(text)
	text = strings.TrimRightFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".,!?;:", r)
	})
	text = strings.TrimSpace(strings.TrimPrefix(text, "/"))
	name, args, _ := strings.Cut(text, " ")
	return Command{Name: strings.ToLower(name), Args: strings.Join(strings.Fields(args), " ")}
}

// Full returns the command word and arguments joined, used for name lookups
// such as "/alice smith".
func (c Command) Full() string {
	if c.Args == "" {
		return c.Name
	}
	return c.Name + " " + strings.ToLower(c.Args)
}
