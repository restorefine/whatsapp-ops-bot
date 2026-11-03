package config

import (
	"strings"
	"testing"
)

func TestVideoTeamIsRosterNotAnotherRole(t *testing.T) {
	env := validEnv()
	env["TEAM_WA_NUMBERS"] = "arpan=9779811111111"
	env["VIDEO_TEAM_WA_NUMBERS"] = "arpan=9779811111111,sunil=9779822222222,himal=9779833333333,"
	c, err := Load(envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.VideoTeam) != 3 || len(c.Team) != 1 || len(c.Admins) != 2 || c.VideoTeam[0].Name != "arpan" {
		t.Fatal("roster changed access roles")
	}
	env["VIDEO_TEAM_WA_NUMBERS"] += "arpan=9779844444444"
	if _, err := Load(envFrom(env)); err == nil || !strings.Contains(err.Error(), "unique names") {
		t.Fatal("duplicate editor mapping accepted")
	}
}
