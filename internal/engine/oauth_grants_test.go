package engine

import (
	"path/filepath"
	"testing"

	"github.com/Ucok23/rekam/internal/db"
)

// Scenarios: spec/oauth-token.md (OAUT-06..09, OAUT-11..13)

// setupGrantTeam gives alice a team with a record in it, and returns the team.
func setupGrantTeam(t *testing.T, eng *Engine, aliceKey, dir string) *db.Team {
	t.Helper()
	team, err := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	if err != nil {
		t.Fatal(err)
	}
	bumpSeats(t, eng, team.ID)
	tok := teamToken(eng, idOf(t, eng, aliceKey), team.ID)
	if _, err := eng.WriteMemory(tok, WriteInput{Title: "Team Note", Taxonomy: "work"}); err != nil {
		t.Fatal(err)
	}
	return team
}

// The whole point of the delegated grant: a token minted for a workspace acts
// inside it, as the user, with the user's role there. Before this, an OAuth
// connection was a synthetic identity that no team had ever heard of.
func TestGrantPinnedToTeamActsInThatTeam(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	team := setupGrantTeam(t, eng, aliceKey, dir)
	aliceID := idOf(t, eng, aliceKey)
	if _, err := eng.WriteMemory(aliceKey, WriteInput{Title: "Home Note", Taxonomy: "personal"}); err != nil {
		t.Fatal(err)
	}

	// OAUT-06
	key, _, err := eng.MintGrantByID(aliceID, "claude", team.ID)
	if err != nil {
		t.Fatal(err)
	}

	// It reads the team's corpus, not the personal one.
	res, err := eng.Search(key, "Team", "", 0)
	if err != nil {
		t.Fatalf("pinned grant should read its team: %v", err)
	}
	if len(res.Results) != 1 {
		t.Errorf("team search = %d, want 1", len(res.Results))
	}
	if home, err := eng.Search(key, "Home Note", "", 0); err != nil || len(home.Results) != 0 {
		t.Errorf("a team-pinned grant must not see personal memory: %v %v", err, home)
	}

	// And it acts as alice, with her role there.
	me, err := eng.MyMembership(key)
	if err != nil {
		t.Fatal(err)
	}
	if me.IdentityID != aliceID || me.Role != db.RoleOwner {
		t.Errorf("grant membership = %s/%s, want %s/owner", me.IdentityID, me.Role, aliceID)
	}
}

// A grant carries the user's role, not a blanket one: an editor's token is an
// editor's token.
func TestGrantCarriesTheUsersRoleNotMore(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	team := setupGrantTeam(t, eng, aliceKey, dir)
	if _, err := eng.registry.Create("bob", "bob-key", filepath.Join(dir, "bob.rekam"), true); err != nil {
		t.Fatal(err)
	}
	bobID := idOf(t, eng, "bob-key")
	aliceTok := teamToken(eng, idOf(t, eng, aliceKey), team.ID)
	if _, err := eng.SetMember(aliceTok, bobID, db.RoleViewer, []string{"work"}, nil); err != nil {
		t.Fatal(err)
	}

	// OAUT-08
	key, _, err := eng.MintGrantByID(bobID, "claude", team.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Search(key, "Team", "", 0); err != nil {
		t.Errorf("viewer's grant should read granted branches: %v", err)
	}
	if _, err := eng.WriteMemory(key, WriteInput{Title: "Nope", Taxonomy: "work"}); err == nil {
		t.Error("a viewer's grant must not write")
	}
	if _, err := eng.SetMember(key, bobID, db.RoleOwner, nil, nil); err == nil {
		t.Error("a viewer's grant must not manage members")
	}
}

func TestGrantWithoutTeamStaysOnPersonalMemory(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	team := setupGrantTeam(t, eng, aliceKey, dir)
	aliceID := idOf(t, eng, aliceKey)
	if _, err := eng.WriteMemory(aliceKey, WriteInput{Title: "Home Note", Taxonomy: "personal"}); err != nil {
		t.Fatal(err)
	}

	// OAUT-07
	key, _, err := eng.MintGrantByID(aliceID, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	home, err := eng.Search(key, "Home", "", 0)
	if err != nil || len(home.Results) != 1 {
		t.Fatalf("unpinned grant should read personal memory: %v %v", err, home)
	}
	if res, err := eng.Search(key, "Team Note", "", 0); err != nil || len(res.Results) != 0 {
		t.Errorf("unpinned grant must not see team memory: %v %v", err, res)
	}

	// It can still enumerate workspaces, with personal marked active — that is how
	// an agent learns another workspace exists and needs its own connection.
	teams, err := eng.MyTeams(key)
	if err != nil {
		t.Fatal(err)
	}
	var sawTeam, activeHome bool
	for _, ref := range teams {
		if ref.ID == team.ID {
			sawTeam = true
		}
		if ref.Home && ref.Active {
			activeHome = true
		}
	}
	if !sawTeam {
		t.Error("a delegated grant should see the user's teams listed")
	}
	if !activeHome {
		t.Error("personal memory should be marked active for an unpinned grant")
	}
}

func TestMyTeamsMarksThePinnedWorkspaceActive(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	team := setupGrantTeam(t, eng, aliceKey, dir)
	key, _, err := eng.MintGrantByID(idOf(t, eng, aliceKey), "claude", team.ID)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := eng.MyTeams(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range teams {
		want := ref.ID == team.ID
		if ref.Active != want {
			t.Errorf("workspace %q active = %v, want %v", ref.Name, ref.Active, want)
		}
	}
}

func TestGrantCannotBePinnedToATeamYouAreNotIn(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	team := setupGrantTeam(t, eng, aliceKey, dir)
	if _, err := eng.registry.Create("mallory", "mallory-key", filepath.Join(dir, "mallory.rekam"), true); err != nil {
		t.Fatal(err)
	}
	malloryID := idOf(t, eng, "mallory-key")

	// OAUT-09
	if _, _, err := eng.MintGrantByID(malloryID, "claude", team.ID); err == nil {
		t.Fatal("minting a grant for someone else's team must fail")
	}
	if _, _, err := eng.MintGrantByID(malloryID, "claude", "no-such-team"); err == nil {
		t.Error("minting a grant for an unknown team must fail")
	}
}

// Revocation used to delete the grant's identity. For a delegated grant that
// identity is a real person, so this guards against revoking a connector wiping
// the account behind it.
func TestRevokingDelegatedGrantKillsTheKeyNotTheUser(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	team := setupGrantTeam(t, eng, aliceKey, dir)
	aliceID := idOf(t, eng, aliceKey)
	key, grantID, err := eng.MintGrantByID(aliceID, "claude", team.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Search(key, "Team", "", 0); err != nil {
		t.Fatalf("grant should work before revocation: %v", err)
	}

	// OAUT-11
	if err := eng.RevokeGrant(grantID); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Search(key, "Team", "", 0); err == nil {
		t.Error("a revoked grant key must stop resolving")
	}
	if _, err := eng.registry.ResolveByID(aliceID); err != nil {
		t.Errorf("revoking a grant must not delete the user it delegated for: %v", err)
	}
	if _, err := eng.MyTeams(aliceKey); err != nil {
		t.Errorf("the user's own key must keep working after revoking a grant: %v", err)
	}
}

// Grants minted before delegation existed own a hidden identity and must keep
// resolving to it, or every already-connected client breaks on upgrade.
func TestLegacyGrantStillResolves(t *testing.T) {
	eng, aliceKey, _, cleanup := setupMultiTeam(t)
	defer cleanup()

	alice, err := eng.registry.ResolveByID(idOf(t, eng, aliceKey))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteMemory(aliceKey, WriteInput{Title: "Home Note", Taxonomy: "personal"}); err != nil {
		t.Fatal(err)
	}
	// OAUT-12
	g, legacyKey, err := eng.registry.MintGrant("claude", "legacy", alice.MemoryPath, true)
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.Search(legacyKey, "Home", "", 0)
	if err != nil || len(res.Results) != 1 {
		t.Fatalf("legacy grant should still read the home corpus it points at: %v %v", err, res)
	}
	// And revoking one still removes its hidden identity, as before.
	if err := eng.RevokeGrant(g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Search(legacyKey, "Home", "", 0); err == nil {
		t.Error("a revoked legacy grant must stop resolving")
	}
}

// The admin console answers "which key can reach what" from ListGrants, so the
// listing has to carry the pin itself, not just the account.
func TestListGrantsReportsThePinnedWorkspace(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	team := setupGrantTeam(t, eng, aliceKey, dir)
	aliceID := idOf(t, eng, aliceKey)
	if _, _, err := eng.MintGrantByID(aliceID, "claude", team.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := eng.MintGrantByID(aliceID, "claude", ""); err != nil {
		t.Fatal(err)
	}

	// OAUT-13
	grants, err := eng.ListGrants()
	if err != nil {
		t.Fatal(err)
	}
	var pinned, personal int
	for _, g := range grants {
		switch g.TeamID {
		case team.ID:
			pinned++
			if g.TeamName != "Acme" {
				t.Errorf("pinned grant team name = %q, want Acme", g.TeamName)
			}
		case "":
			personal++
		default:
			t.Errorf("unexpected team id %q", g.TeamID)
		}
	}
	if pinned != 1 || personal != 1 {
		t.Errorf("got %d pinned + %d personal grants, want 1 each", pinned, personal)
	}

	// A grant outliving its team reads as pinned-but-nameless, which is what tells
	// an admin the key points at something deleted rather than at personal memory.
	if err := eng.DeleteTeam(teamToken(eng, aliceID, team.ID)); err != nil {
		t.Fatal(err)
	}
	grants, err = eng.ListGrants()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grants {
		if g.TeamID == team.ID && g.TeamName != "" {
			t.Errorf("deleted team should not resolve to a name, got %q", g.TeamName)
		}
	}
}
