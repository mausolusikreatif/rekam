package db

import "testing"

// Scenarios: spec/teams.md (TEAM-17, TEAM-19)

// TEAM-19
func TestRoleProjectsToScope(t *testing.T) {
	owner := (&Member{Role: RoleOwner}).Scope()
	if !owner.Unrestricted() {
		t.Error("owner must project to FullScope")
	}
	if !(&Member{Role: RoleAdmin}).Scope().Unrestricted() {
		t.Error("admin must project to FullScope")
	}

	ed := (&Member{Role: RoleEditor, ReadGrants: []string{"eng"}, TitleGrants: []string{"finance"}}).Scope()
	if ed.Unrestricted() {
		t.Fatal("editor must be restricted")
	}
	if !ed.CanRead("eng.ops") || !ed.CanWrite("eng.ops") {
		t.Error("editor reads and writes its granted branch")
	}
	if ed.CanWrite("finance.q3") || ed.CanRead("finance.q3") {
		t.Error("editor must not read/write outside grants")
	}
	if !ed.TitleVisible("finance.q3") {
		t.Error("editor sees titles in its title grant")
	}

	vw := (&Member{Role: RoleViewer, ReadGrants: []string{"eng"}}).Scope()
	if !vw.CanRead("eng.ops") {
		t.Error("viewer reads its granted branch")
	}
	if vw.CanWrite("eng.ops") {
		t.Error("viewer must never write, even in its read grant")
	}
}

func TestRoleCapabilities(t *testing.T) {
	cases := []struct {
		role                         Role
		write, manageMem, manageTeam bool
	}{
		{RoleOwner, true, true, true},
		{RoleAdmin, true, true, false},
		{RoleEditor, true, false, false},
		{RoleViewer, false, false, false},
	}
	for _, c := range cases {
		if c.role.CanWriteAny() != c.write {
			t.Errorf("%s CanWriteAny = %v, want %v", c.role, c.role.CanWriteAny(), c.write)
		}
		if c.role.CanManageMembers() != c.manageMem {
			t.Errorf("%s CanManageMembers = %v, want %v", c.role, c.role.CanManageMembers(), c.manageMem)
		}
		if c.role.CanManageTeam() != c.manageTeam {
			t.Errorf("%s CanManageTeam = %v, want %v", c.role, c.role.CanManageTeam(), c.manageTeam)
		}
	}
}

// TestEffectiveMemberSoloFallback: a file with no members treats its user as
// Owner, so a personal store needs zero team setup.
func TestEffectiveMemberSoloFallback(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	member, denied, err := mdb.EffectiveMember("anyone")
	if err != nil {
		t.Fatal(err)
	}
	if denied {
		t.Fatal("a member-less file must not deny its user")
	}
	if member.Role != RoleOwner || !member.Scope().Unrestricted() {
		t.Errorf("solo user = %+v, want Owner/FullScope", member)
	}
}

// TestEffectiveMemberDeniesNonMember is the security-critical case: once a file
// is a team, an identity without a row gets nothing — never the solo fallback.
func TestEffectiveMemberDeniesNonMember(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	if err := mdb.PutMember("owner-id", RoleOwner, nil, nil, ""); err != nil {
		t.Fatal(err)
	}

	// The owner resolves to their role.
	member, denied, err := mdb.EffectiveMember("owner-id")
	if err != nil || denied {
		t.Fatalf("owner resolve: member=%v denied=%v err=%v", member, denied, err)
	}
	if member.Role != RoleOwner {
		t.Errorf("role = %s, want owner", member.Role)
	}

	// A stranger is denied, NOT defaulted to owner.
	_, denied, err = mdb.EffectiveMember("stranger-id")
	if err != nil {
		t.Fatal(err)
	}
	if !denied {
		t.Fatal("non-member of a team file must be denied, not granted the solo fallback")
	}
}

func TestPutMemberUpsertAndGrants(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	if err := mdb.PutMember("u", RoleEditor, []string{"eng", "docs"}, []string{"finance"}, "admin"); err != nil {
		t.Fatal(err)
	}
	m, err := mdb.Member("u")
	if err != nil || m == nil {
		t.Fatalf("member u: %v", err)
	}
	if m.Role != RoleEditor || len(m.ReadGrants) != 2 || len(m.TitleGrants) != 1 {
		t.Errorf("member = %+v, want editor with 2 read + 1 title grant", m)
	}
	if m.AddedBy != "admin" {
		t.Errorf("added_by = %q, want admin", m.AddedBy)
	}

	// Upsert: change role and replace grants; added_at/added_by must persist.
	origAdded := m.AddedAt
	if err := mdb.PutMember("u", RoleViewer, []string{"eng"}, nil, "someone-else"); err != nil {
		t.Fatal(err)
	}
	m2, _ := mdb.Member("u")
	if m2.Role != RoleViewer || len(m2.ReadGrants) != 1 || len(m2.TitleGrants) != 0 {
		t.Errorf("after upsert = %+v, want viewer with 1 read grant and no titles", m2)
	}
	if m2.AddedAt != origAdded || m2.AddedBy != "admin" {
		t.Errorf("upsert must preserve original added_at/added_by, got %s/%s", m2.AddedAt, m2.AddedBy)
	}
}

func TestCountOwnersAndListOrder(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mustPut := func(id string, r Role) {
		if err := mdb.PutMember(id, r, nil, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	mustPut("v", RoleViewer)
	mustPut("o1", RoleOwner)
	mustPut("e", RoleEditor)
	mustPut("o2", RoleOwner)
	mustPut("a", RoleAdmin)

	n, err := mdb.CountOwners()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("owners = %d, want 2", n)
	}

	members, err := mdb.ListMembers()
	if err != nil {
		t.Fatal(err)
	}
	// Owners first, then admin, editor, viewer.
	if members[0].Role != RoleOwner || members[len(members)-1].Role != RoleViewer {
		t.Errorf("roster order wrong: first=%s last=%s", members[0].Role, members[len(members)-1].Role)
	}
}

// TEAM-17
func TestPutMemberRejectsUnknownRole(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()
	if err := mdb.PutMember("u", Role("superuser"), nil, nil, ""); err == nil {
		t.Error("expected an error for an unknown role")
	}
}
