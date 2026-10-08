package config

import (
	"strings"
	"testing"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validEnv() map[string]string {
	return map[string]string{
		"WA_PHONE_NUMBER_ID": "123",
		"WA_TOKEN":           "tok",
		"WA_APP_SECRET":      "secret",
		"WA_VERIFY_TOKEN":    "verify",
		"OWNER_WA_NUMBER":    "+9779812345678, 447700900123",
		"CLICKUP_TOKEN":      "pk_1",
		"CLICKUP_TEAM_ID":    "42",
	}
}

func TestLoadValid(t *testing.T) {
	c, err := Load(envFrom(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if n := c.AdminNumbers(); len(n) != 2 || n[0] != "9779812345678" || n[1] != "447700900123" {
		t.Errorf("admins = %q, want both legacy owner numbers with plus sign and spaces stripped", n)
	}
	if c.TZLabel != "UK" || c.SecondTZ == nil || c.SecondTZ.String() != "Asia/Kathmandu" || c.SecondLabel != "Nepal" {
		t.Errorf("time zone labels = %q, %v, %q", c.TZLabel, c.SecondTZ, c.SecondLabel)
	}
	if c.WAGraphVersion != "v25.0" || c.Location.String() != "Europe/London" || c.HTTPAddr != ":8080" {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestLoadListsEveryMissingVariable(t *testing.T) {
	_, err := Load(envFrom(map[string]string{}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, key := range []string{"WA_PHONE_NUMBER_ID", "WA_TOKEN", "WA_APP_SECRET", "WA_VERIFY_TOKEN", "ADMIN_WA_NUMBERS", "CLICKUP_TOKEN", "CLICKUP_TEAM_ID"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s:\n%s", key, err)
		}
	}
}

func TestLoadDryRunSkipsOutboundCredentials(t *testing.T) {
	env := validEnv()
	delete(env, "WA_TOKEN")
	delete(env, "WA_PHONE_NUMBER_ID")
	env["WA_DRY_RUN"] = "true"
	if _, err := Load(envFrom(env)); err != nil {
		t.Fatal(err)
	}
}

func TestLoadInvalidValues(t *testing.T) {
	tests := map[string]string{
		"OWNER_WA_NUMBER":  "98-123",
		"TZ":               "Mars/Olympus",
		"WA_GRAPH_VERSION": "25",
		"LOG_LEVEL":        "loud",
	}
	for key, val := range tests {
		t.Run(key, func(t *testing.T) {
			env := validEnv()
			env[key] = val
			_, err := Load(envFrom(env))
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected error mentioning %s, got %v", key, err)
			}
		})
	}
}

func TestLoadTeamNumbers(t *testing.T) {
	env := validEnv()
	env["OWNER_WA_NUMBER"] = "447700900999"
	env["TEAM_WA_NUMBERS"] = " Sunil = +447700900123, sunil paudel=9779812345678 ,"
	c, err := Load(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	want := []Person{{"sunil", "447700900123"}, {"sunil paudel", "9779812345678"}}
	if len(c.Team) != 2 || c.Team[0] != want[0] || c.Team[1] != want[1] {
		t.Errorf("team = %+v, want %+v", c.Team, want)
	}

	for _, bad := range []string{"sunil", "=447700900123", "sunil=07700 900123"} {
		env["TEAM_WA_NUMBERS"] = bad
		if _, err := Load(envFrom(env)); err == nil || !strings.Contains(err.Error(), "TEAM_WA_NUMBERS") {
			t.Errorf("%q: expected TEAM_WA_NUMBERS error, got %v", bad, err)
		}
	}
}

func TestLoadRoles(t *testing.T) {
	env := validEnv()
	delete(env, "OWNER_WA_NUMBER")
	env["ADMIN_WA_NUMBERS"] = "suranjana=9779812378182, 447590990552"
	env["TEAM_WA_NUMBERS"] = "sagun=9779860906634"
	c, err := Load(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	want := []Person{{"suranjana", "9779812378182"}, {"", "447590990552"}}
	if len(c.Admins) != 2 || c.Admins[0] != want[0] || c.Admins[1] != want[1] {
		t.Errorf("admins = %+v, want %+v", c.Admins, want)
	}

	env["TEAM_WA_NUMBERS"] = "sagun=9779860906634,suranjana=9779812378182"
	if _, err := Load(envFrom(env)); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Errorf("a number in two roles should be rejected, got %v", err)
	}

	env["TEAM_WA_NUMBERS"] = "9779860906634"
	if _, err := Load(envFrom(env)); err == nil || !strings.Contains(err.Error(), "TEAM_WA_NUMBERS") {
		t.Errorf("team entries need a name, got %v", err)
	}

	env["TEAM_WA_NUMBERS"], env["SECOND_TZ"] = "", "off"
	if c, err := Load(envFrom(env)); err != nil || c.SecondTZ != nil {
		t.Errorf("SECOND_TZ=off: %v, %v", c, err)
	}
}
