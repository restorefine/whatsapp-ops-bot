package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRosterValidationBeforeOnboarding(t *testing.T) {
	for _, tc := range []struct {
		name, csv string
		valid     bool
	}{{"multiple contacts", "client,whatsapp\nBlanc Events,447700900111;9779812345678\nOther client,\n", true}, {"duplicate", "client,whatsapp\nBlanc Events,447700900111\nblanc events,447700900222\n", false}, {"bad number", "client,whatsapp\nBlanc Events,not-a-number\n", false}, {"path name", "client,whatsapp\n../outside,447700900111\n", false}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "clients.csv")
			if err := os.WriteFile(path, []byte(tc.csv), 0600); err != nil {
				t.Fatal(err)
			}
			rows, err := readRoster(path)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && (len(rows) != 2 || len(rows[0].numbers) != 2) {
				t.Fatal("contacts were not parsed")
			}
		})
	}
}

func TestSigningKeyIsPrivateAndStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "key")
	first, err := signingKey(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := signingKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || len(first) != 32 {
		t.Fatal("key changed on repeat setup")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("signing key is not private")
	}
}
