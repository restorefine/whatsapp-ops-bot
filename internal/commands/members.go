package commands

import (
	"sort"
	"strings"

	"github.com/prabishdangi/whatsapp-ops-bot/internal/clickup"
)

// MatchMembers finds the members a query like "alice", "a" or "alice smith"
// refers to. It tries, in order: exact full name (with or without spaces) or
// email name, exact first name, then any name word or email starting with the
// query. The first tier with matches wins.
func MatchMembers(members []clickup.Member, query string) []clickup.Member {
	q := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if q == "" {
		return nil
	}
	qNoSpace := strings.ReplaceAll(q, " ", "")

	var exact, first, prefix []clickup.Member
	for _, m := range members {
		name := strings.ToLower(strings.Join(strings.Fields(m.Username), " "))
		words := strings.Fields(name)
		emailName, _, _ := strings.Cut(strings.ToLower(m.Email), "@")

		switch {
		case name != "" && (q == name || qNoSpace == strings.ReplaceAll(name, " ", "")),
			emailName != "" && qNoSpace == emailName:
			exact = append(exact, m)
		case len(words) > 0 && q == words[0]:
			first = append(first, m)
		case hasPrefix(words, q) || strings.HasPrefix(name, q) || (emailName != "" && strings.HasPrefix(emailName, qNoSpace)):
			prefix = append(prefix, m)
		}
	}
	for _, tier := range [][]clickup.Member{exact, first, prefix} {
		if len(tier) > 0 {
			sortMembers(tier)
			return tier
		}
	}
	return nil
}

func hasPrefix(words []string, q string) bool {
	for _, w := range words {
		if strings.HasPrefix(w, q) {
			return true
		}
	}
	return false
}

func sortMembers(ms []clickup.Member) {
	sort.Slice(ms, func(i, j int) bool {
		return strings.ToLower(ms[i].DisplayName()) < strings.ToLower(ms[j].DisplayName())
	})
}

// ShortCommand is the shortest command that selects exactly m: the first name
// when it is unique, then the full name without spaces, then the email name
// (for two people with the same name).
func ShortCommand(members []clickup.Member, m clickup.Member) string {
	name := strings.ToLower(strings.Join(strings.Fields(m.Username), " "))
	first, _, _ := strings.Cut(name, " ")
	emailName, _, _ := strings.Cut(strings.ToLower(m.Email), "@")
	candidates := []string{first, strings.ReplaceAll(name, " ", ""), emailName}
	for _, c := range candidates {
		if c == "" || reserved[c] {
			continue
		}
		if found := MatchMembers(members, c); len(found) == 1 && found[0].ID == m.ID {
			return "/" + c
		}
	}
	for _, c := range candidates[1:] {
		if c != "" {
			return "/" + c
		}
	}
	return "/" + first
}
