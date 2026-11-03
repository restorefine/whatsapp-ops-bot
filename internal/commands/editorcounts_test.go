package commands

import (
	"context"
	"testing"
)

func TestEditorCountsOnlyReachCallbackForAdmins(t *testing.T) {
	r, _ := newRouter(&fakeClickUp{})
	calls := 0
	r.EditorCounts = func(_ context.Context, number, args string) string {
		calls++
		if number != "admin-number" || args != "sunil" {
			t.Fatal("admin identity or editor filter lost")
		}
		return "private-report"
	}
	for _, role := range []Role{RoleNone, RoleTeam} {
		reply := r.Handle(context.Background(), Contact{Name: "sunil", Number: "team-number"}, role, "/editors sunil")
		if calls != 0 || reply == "private-report" {
			t.Fatal("team member obtained count or admin link")
		}
	}
	if reply := r.Handle(context.Background(), Contact{Number: "admin-number"}, RoleAdmin, "/editors sunil"); reply != "private-report" || calls != 1 {
		t.Fatal("admin command not routed")
	}
}
