package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Ucok23/rekam/internal/db"
)

// Scenarios: spec/teams.md (TEAM-01, TEAM-03, TEAM-04, TEAM-05, TEAM-07,
// TEAM-08, TEAM-09, TEAM-10, TEAM-11, TEAM-12, TEAM-13, TEAM-18, TEAM-19,
// TEAM-20, TEAM-21)

// setupMultiTeam builds an engine with a temp dir for team files and a single
// registered identity (alice) whose home is her personal corpus. Teams are
// created through the engine, the way the product will.
func setupMultiTeam(t *testing.T) (eng *Engine, aliceKey string, dir string, cleanup func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "rekam-teams-")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := db.OpenRegistry(filepath.Join(dir, "registry.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	aliceKey = "alice-key"
	if _, err := reg.Create("alice", aliceKey, filepath.Join(dir, "alice.rekam"), true); err != nil {
		t.Fatal(err)
	}
	eng = New(reg, 6000)
	cleanup = func() {
		reg.Close()
		os.RemoveAll(dir)
	}
	return eng, aliceKey, dir, cleanup
}

func idOf(t *testing.T, eng *Engine, key string) string {
	t.Helper()
	id, err := eng.registry.Resolve(key)
	if err != nil {
		t.Fatal(err)
	}
	return id.ID
}

// teamToken mints the team-bound bearer the transport layer would produce for a
// (identity, team) selection, so tests can act inside a chosen team.
func teamToken(eng *Engine, identityID, teamID string) string {
	return eng.SessionTokenForTeam(identityID, teamID)
}

// bumpSeats raises a freshly created team past its default free seat_limit
// (1 — the Owner alone) so a test can invite additional members. Every new
// team starts on the free plan; tests exercising multi-member mechanics
// aren't testing that default, so they opt out of it here rather than
// tripping over it.
func bumpSeats(t *testing.T, eng *Engine, teamID string) {
	t.Helper()
	if err := eng.SetTeamPlan(teamID, "team", 10); err != nil {
		t.Fatal(err)
	}
}

func TestCreateTeamSeedsOwnerAndIsSeparateFromHome(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	team, err := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	if err != nil {
		t.Fatal(err)
	}
	aliceID := idOf(t, eng, aliceKey)
	tok := teamToken(eng, aliceID, team.ID)

	// TEAM-01, TEAM-09: Alice is Owner of the new team.
	me, err := eng.MyMembership(tok)
	if err != nil {
		t.Fatal(err)
	}
	if me.Role != db.RoleOwner {
		t.Errorf("creator role = %s, want owner", me.Role)
	}

	// TEAM-04: a memory written in the team lives in the team, not in her personal home.
	if _, err := eng.WriteMemory(tok, WriteInput{Title: "Team Note", Taxonomy: "work"}); err != nil {
		t.Fatal(err)
	}
	teamList, err := eng.Search(tok, "Team", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(teamList.Results) != 1 {
		t.Errorf("team search = %d, want 1", len(teamList.Results))
	}
	// Home (no team selector) must not see the team's memory — different corpus.
	homeList, err := eng.Search(aliceKey, "Team", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(homeList.Results) != 0 {
		t.Errorf("home search saw %d team memories; corpora must be isolated", len(homeList.Results))
	}
}

func TestInviteIsAdditiveAcrossTeams(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	// Bob has his own home corpus with a personal memory.
	if _, err := eng.registry.Create("bob", "bob-key", filepath.Join(dir, "bob.rekam"), true); err != nil {
		t.Fatal(err)
	}
	bobID := idOf(t, eng, "bob-key")
	if _, err := eng.WriteMemory("bob-key", WriteInput{Title: "Bob Private", Taxonomy: "personal"}); err != nil {
		t.Fatal(err)
	}

	// Alice creates a team and invites Bob as Editor scoped to "work".
	team, err := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	if err != nil {
		t.Fatal(err)
	}
	bumpSeats(t, eng, team.ID)
	aliceTok := teamToken(eng, idOf(t, eng, aliceKey), team.ID)
	// TEAM-13
	if _, err := eng.SetMember(aliceTok, bobID, db.RoleEditor, []string{"work"}, nil); err != nil {
		t.Fatal(err)
	}

	// TEAM-13: Bob's HOME is untouched — the invite added, it did not move him.
	bobHome, err := eng.Search("bob-key", "Private", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobHome.Results) != 1 {
		t.Errorf("bob's home lost its memory to the invite: got %d, want 1", len(bobHome.Results))
	}

	// Acting in the team, Bob is an Editor of "work".
	bobTeamTok := teamToken(eng, bobID, team.ID)
	me, err := eng.MyMembership(bobTeamTok)
	if err != nil {
		t.Fatal(err)
	}
	if me.Role != db.RoleEditor {
		t.Errorf("bob team role = %s, want editor", me.Role)
	}
	// TEAM-19
	if _, err := eng.WriteMemory(bobTeamTok, WriteInput{Title: "Spec", Taxonomy: "work.specs"}); err != nil {
		t.Errorf("editor should write in granted branch: %v", err)
	}
	// TEAM-19: but not outside his grant.
	if _, err := eng.WriteMemory(bobTeamTok, WriteInput{Title: "X", Taxonomy: "finance"}); err == nil {
		t.Error("editor must not write outside granted branch")
	}
}

func TestMyTeamsListsHomeAndJoinedTeams(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	if _, err := eng.registry.Create("bob", "bob-key", filepath.Join(dir, "bob.rekam"), true); err != nil {
		t.Fatal(err)
	}
	bobID := idOf(t, eng, "bob-key")

	t1, _ := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	t2, _ := eng.CreateTeam(aliceKey, "Globex", filepath.Join(dir, "globex.rekam"))
	bumpSeats(t, eng, t1.ID)
	aliceAcme := teamToken(eng, idOf(t, eng, aliceKey), t1.ID)
	if _, err := eng.SetMember(aliceAcme, bobID, db.RoleViewer, []string{"work"}, nil); err != nil {
		t.Fatal(err)
	}

	// TEAM-03: Alice: home + two teams she owns.
	aliceTeams, err := eng.MyTeams(aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceTeams) != 3 || !aliceTeams[0].Home {
		t.Fatalf("alice teams = %+v, want home + 2", aliceTeams)
	}
	names := map[string]db.Role{}
	for _, tr := range aliceTeams {
		names[tr.Name] = tr.Role
	}
	if names["Acme"] != db.RoleOwner || names["Globex"] != db.RoleOwner {
		t.Errorf("alice should own both teams: %v", names)
	}
	_ = t2

	// TEAM-03: Bob: home + only Acme (as Viewer). Globex must not appear.
	bobTeams, err := eng.MyTeams("bob-key")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]db.Role{}
	for _, tr := range bobTeams {
		seen[tr.Name] = tr.Role
	}
	if _, ok := seen["Globex"]; ok {
		t.Error("bob must not see a team he was never added to")
	}
	if seen["Acme"] != db.RoleViewer {
		t.Errorf("bob's Acme role = %s, want viewer", seen["Acme"])
	}
}

// TestLookupMemberByEmail: a manager can resolve an exact email to an invitable
// identity, but the lookup is fenced — no team context, an unknown address, or a
// non-managing role all fail, so it can't be used to harvest the account directory.
func TestLookupMemberByEmail(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	// Bob has an email-backed account (self-service style).
	bob, _, err := eng.registry.CreateUser("Bob", "bob@acme.test", "password123",
		filepath.Join(dir, "bob.rekam"), true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	team, _ := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	bumpSeats(t, eng, team.ID)
	aliceTok := teamToken(eng, idOf(t, eng, aliceKey), team.ID)

	// TEAM-11: exact match resolves to Bob's identity.
	cand, err := eng.LookupMemberByEmail(aliceTok, "BOB@acme.test") // case-insensitive
	if err != nil {
		t.Fatalf("manager lookup failed: %v", err)
	}
	if cand.IdentityID != bob.ID {
		t.Errorf("lookup id = %s, want %s", cand.IdentityID, bob.ID)
	}

	// TEAM-11: unknown address is not_found — indistinguishable from malformed.
	if _, err := eng.LookupMemberByEmail(aliceTok, "nobody@acme.test"); err == nil {
		t.Error("unknown email should be not_found")
	}

	// TEAM-12: no team context (home) is forbidden, so it isn't a universal existence oracle.
	if _, err := eng.LookupMemberByEmail(aliceKey, "bob@acme.test"); err == nil {
		t.Error("lookup without a team must be forbidden")
	}

	// TEAM-12: a non-managing role cannot look up: add Bob as Viewer and try as him.
	if _, err := eng.SetMember(aliceTok, bob.ID, db.RoleViewer, []string{"work"}, nil); err != nil {
		t.Fatal(err)
	}
	bobTok := teamToken(eng, bob.ID, team.ID)
	if _, err := eng.LookupMemberByEmail(bobTok, "bob@acme.test"); err == nil {
		t.Error("a viewer must not be able to look up members")
	}
}

// TestTeamSelectorForgeryRejected: the team id is signed into the session token,
// so a tampered selector fails signature verification rather than routing.
func TestTeamSelectorForgeryRejected(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()
	team, _ := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	aliceID := idOf(t, eng, aliceKey)

	good := teamToken(eng, aliceID, team.ID)
	forged := good[:len(good)-3] + "zzz" // corrupt the signature tail
	// TEAM-18
	if _, err := eng.MyMembership(forged); err == nil {
		t.Error("a token with a broken signature must not authenticate")
	}
}

// TestNonMemberCannotSelectTeam: knowing a team id is not access — routing into
// it still hits the tenant membership gate.
func TestNonMemberCannotSelectTeam(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	// Mallory is a valid identity but never added to the team.
	if _, err := eng.registry.Create("mallory", "mal-key", filepath.Join(dir, "mal.rekam"), true); err != nil {
		t.Fatal(err)
	}
	team, _ := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	malID := idOf(t, eng, "mal-key")

	// She mints a (validly signed) team token for a team she's not in.
	// TEAM-18
	tok := teamToken(eng, malID, team.ID)
	_, err := eng.Search(tok, "anything", "", 0)
	if ae, ok := err.(*ActionableError); !ok || ae.Code != "not_a_member" {
		t.Fatalf("non-member team access = %v, want not_a_member", err)
	}
}

func TestRosterResolvesDisplayIdentities(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	bob, _, err := eng.registry.CreateUser("bob", "bob@example.com", "pw-correct-horse",
		filepath.Join(dir, "bob.rekam"), true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	team, err := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	if err != nil {
		t.Fatal(err)
	}
	bumpSeats(t, eng, team.ID)
	tok := teamToken(eng, idOf(t, eng, aliceKey), team.ID)
	if _, err := eng.SetMember(tok, bob.ID, db.RoleEditor, []string{"work"}, nil); err != nil {
		t.Fatal(err)
	}

	// TEAM-10
	members, err := eng.TeamMembers(tok)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*TeamMember{}
	for _, m := range members {
		byID[m.IdentityID] = m
	}
	if got := byID[bob.ID]; got == nil {
		t.Fatal("bob missing from roster")
	} else if got.Name != "bob" || got.Email != "bob@example.com" {
		t.Errorf("roster row = %q/%q, want bob/bob@example.com — a bare uuid is unreadable", got.Name, got.Email)
	}
	if got := byID[idOf(t, eng, aliceKey)]; got == nil || got.Name != "alice" {
		t.Error("owner row should carry its display name too")
	}
}

func TestRenameTeamByManagerAndDeleteByOwnerOnly(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	if _, err := eng.registry.Create("bob", "bob-key", filepath.Join(dir, "bob.rekam"), true); err != nil {
		t.Fatal(err)
	}
	bobID := idOf(t, eng, "bob-key")
	team, err := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	if err != nil {
		t.Fatal(err)
	}
	bumpSeats(t, eng, team.ID)
	aliceTok := teamToken(eng, idOf(t, eng, aliceKey), team.ID)
	if _, err := eng.SetMember(aliceTok, bobID, db.RoleAdmin, nil, nil); err != nil {
		t.Fatal(err)
	}
	bobTok := teamToken(eng, bobID, team.ID)

	// TEAM-05: an Admin may rename: the name is a label, not a capability.
	if _, err := eng.RenameTeam(bobTok, "Acme Research"); err != nil {
		t.Errorf("admin should rename: %v", err)
	}
	renamed, err := eng.registry.TeamByID(team.ID)
	if err != nil || renamed.Name != "Acme Research" {
		t.Errorf("rename did not stick: %v %+v", err, renamed)
	}
	if _, err := eng.RenameTeam(aliceTok, "   "); err == nil {
		t.Error("a blank name must be rejected")
	}

	// TEAM-08: but an Admin may not delete the team.
	if err := eng.DeleteTeam(bobTok); err == nil {
		t.Error("admin must not delete the team")
	}
	// TEAM-08: nor may anyone delete their personal home, which has no team record.
	if err := eng.DeleteTeam(aliceKey); err == nil {
		t.Error("home is not a deletable team")
	}
	if err := eng.DeleteTeam(aliceTok); err != nil {
		t.Fatalf("owner should delete: %v", err)
	}
}

func TestDeletedTeamIsUnreachableButItsRecordsSurvive(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	path := filepath.Join(dir, "acme.rekam")
	team, err := eng.CreateTeam(aliceKey, "Acme", path)
	if err != nil {
		t.Fatal(err)
	}
	aliceID := idOf(t, eng, aliceKey)
	tok := teamToken(eng, aliceID, team.ID)
	if _, err := eng.WriteMemory(tok, WriteInput{Title: "Team Note", Taxonomy: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.DeleteTeam(tok); err != nil {
		t.Fatal(err)
	}

	// TEAM-07: gone from the switcher: only Personal remains.
	teams, err := eng.MyTeams(aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range teams {
		if ref.ID == team.ID {
			t.Error("deleted team still listed in MyTeams")
		}
	}
	// TEAM-07: and no longer reachable: the id stops resolving to a path, so
	// even the owner's still-valid team-bound token cannot act inside it.
	// (BearerForTeam itself stays permissive by design — resolveTenant is the gate.)
	if _, err := eng.MyMembership(tok); err == nil {
		t.Error("a deleted team must not be reachable with an existing team token")
	}
	if _, err := eng.Search(tok, "Team", "", 0); err == nil {
		t.Error("a deleted team's corpus must not be readable")
	}
	// TEAM-07: deleting twice is an error, not a silent success.
	if err := eng.registry.SoftDeleteTeam(team.ID); err == nil {
		t.Error("re-deleting a deleted team should fail")
	}
	// TEAM-07: the corpus file is untouched, so the deletion is recoverable.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("soft delete must not unlink the corpus: %v", err)
	}
}

// TEAM-20. A fresh team starts on the free plan (seat_limit 1 — the owner
// alone). TestOwnedTeamsCapped (TEAM-21) covers the other half — capping how
// many teams one identity can own — since a per-team seat cap means nothing
// if nothing limits how many teams route around it.
func TestSeatLimitBlocksAdditionalMembers(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	if _, err := eng.registry.Create("bob", "bob-key", filepath.Join(dir, "bob.rekam"), true); err != nil {
		t.Fatal(err)
	}
	bobID := idOf(t, eng, "bob-key")

	team, err := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	if err != nil {
		t.Fatal(err)
	}
	aliceTok := teamToken(eng, idOf(t, eng, aliceKey), team.ID)

	// The owner alone already fills the free plan's 1 seat.
	_, err = eng.SetMember(aliceTok, bobID, db.RoleEditor, []string{"work"}, nil)
	var ae *ActionableError
	if !errors.As(err, &ae) || ae.Code != "seat_limit" {
		t.Fatalf("invite past free seat_limit: want seat_limit error, got %v", err)
	}

	// A role change for someone already seated isn't adding a seat, so it must
	// not be blocked — only a genuinely new member counts against the limit.
	if _, err := eng.SetMember(aliceTok, idOf(t, eng, aliceKey), db.RoleOwner, nil, nil); err != nil {
		t.Errorf("re-setting an existing member's own role should not hit the seat cap: %v", err)
	}

	// Raising the plan's seat_limit (what a Paddle webhook will eventually do)
	// lets the same invite through.
	if err := eng.SetTeamPlan(team.ID, "team", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.SetMember(aliceTok, bobID, db.RoleEditor, []string{"work"}, nil); err != nil {
		t.Errorf("invite after raising seat_limit: %v", err)
	}
}

// TEAM-21. The other half of the seat-cap story: capping how many teams a
// single identity may own, so a "1 seat per team" limit can't be routed
// around by just creating more teams.
func TestOwnedTeamsCapped(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	for i := 0; i < maxOwnedTeams; i++ {
		name := fmt.Sprintf("Team%d", i)
		if _, err := eng.CreateTeam(aliceKey, name, filepath.Join(dir, name+".rekam")); err != nil {
			t.Fatalf("create team %d (within the limit): %v", i, err)
		}
	}
	_, err := eng.CreateTeam(aliceKey, "OneTooMany", filepath.Join(dir, "one-too-many.rekam"))
	var ae *ActionableError
	if !errors.As(err, &ae) || ae.Code != "team_limit" {
		t.Fatalf("create team past the owned-teams cap: want team_limit error, got %v", err)
	}
}

// TEAM-21: the cap holds under concurrent requests, not just sequential ones
// — a caller one seat under the limit firing several POST /teams-equivalent
// calls at once gets exactly one success, never more than the remaining
// seats actually allow. Regression test for a real TOCTOU: the count check
// and the insert used to be two separate round trips (a plain COUNT(*) read,
// then a later INSERT), so racing calls could each read "one seat free"
// before any of them committed and all succeed.
func TestOwnedTeamsCappedUnderConcurrency(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	// Use maxOwnedTeams-1 seats sequentially, leaving exactly one free.
	for i := 0; i < maxOwnedTeams-1; i++ {
		name := fmt.Sprintf("Warm%d", i)
		if _, err := eng.CreateTeam(aliceKey, name, filepath.Join(dir, name+".rekam")); err != nil {
			t.Fatalf("warm-up create %d: %v", i, err)
		}
	}

	const attempts = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes int
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("Race%d", i)
			if _, err := eng.CreateTeam(aliceKey, name, filepath.Join(dir, name+".rekam")); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if successes != 1 {
		t.Errorf("concurrent creates with one seat free = %d successes, want exactly 1", successes)
	}
	owned, err := eng.registry.CountOwnedTeams(mustResolveIdentityID(t, eng, aliceKey))
	if err != nil {
		t.Fatal(err)
	}
	if owned != maxOwnedTeams {
		t.Errorf("final owned count = %d, want exactly %d", owned, maxOwnedTeams)
	}
}

func mustResolveIdentityID(t *testing.T, eng *Engine, apiKey string) string {
	t.Helper()
	id, _, err := eng.resolveBearer(apiKey)
	if err != nil {
		t.Fatal(err)
	}
	return id.ID
}
