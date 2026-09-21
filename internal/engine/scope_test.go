package engine

import (
	"os"
	"testing"

	"github.com/Ucok23/rekam/internal/db"
)

// setupSharedCorpus builds one memory store shared by two identities: an admin
// with full scope, and a restricted "eng" identity that can read+write eng, see
// finance titles, and not touch secret at all. It returns the engine and both
// API keys — the fixture for team-mode policy tests.
func setupSharedCorpus(t *testing.T) (eng *Engine, adminKey, engKey string, cleanup func()) {
	t.Helper()

	regFile, err := os.CreateTemp("", "registry-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	regFile.Close()
	memFile, err := os.CreateTemp("", "shared-*.memory")
	if err != nil {
		os.Remove(regFile.Name())
		t.Fatal(err)
	}
	memFile.Close()

	reg, err := db.OpenRegistry(regFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	adminKey, engKey = "admin-key", "eng-key"
	admin, err := reg.Create("admin", adminKey, memFile.Name(), true)
	if err != nil {
		t.Fatal(err)
	}
	engID, err := reg.Create("eng", engKey, memFile.Name(), true) // SAME path = shared corpus
	if err != nil {
		t.Fatal(err)
	}

	eng = New(reg, 6000)

	// Seed the tenant roster the way team creation will: the creator as Owner,
	// then a scoped Editor. Seeding directly (not via the capability-checked
	// engine API) mirrors bootstrap, where there is no prior manager to authorize.
	mdb, err := eng.GetOrOpenMem(memFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := mdb.PutMember(admin.ID, db.RoleOwner, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := mdb.PutMember(engID.ID, db.RoleEditor, []string{"eng"}, []string{"finance"}, admin.ID); err != nil {
		t.Fatal(err)
	}

	cleanup = func() {
		reg.Close()
		os.Remove(regFile.Name())
		os.Remove(memFile.Name())
	}
	return eng, adminKey, engKey, cleanup
}

// seedShared writes one memory per branch as admin and returns their ids by branch.
func seedShared(t *testing.T, eng *Engine, adminKey string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for branch, spec := range map[string][2]string{
		"eng":     {"Deploy Guide", "eng.ops"},
		"finance": {"Q3 Revenue", "finance.q3"},
		"secret":  {"Acquisition", "secret.mna"},
	} {
		m, err := eng.WriteMemory(adminKey, WriteInput{
			Title: spec[0], Content: "body of " + spec[0], Taxonomy: spec[1],
		})
		if err != nil {
			t.Fatalf("seed %s: %v", branch, err)
		}
		ids[branch] = m.ID
	}
	return ids
}

func TestScopeRoundTripsThroughRegistry(t *testing.T) {
	eng, _, engKey, cleanup := setupSharedCorpus(t)
	defer cleanup()

	// The eng identity resolves to exactly the scope we stored — not FullScope.
	_, _, scope, release, err := eng.resolve(engKey, false)
	defer release()
	if err != nil {
		t.Fatal(err)
	}
	if scope.Unrestricted() {
		t.Fatal("restricted identity must not resolve to FullScope")
	}
	if !scope.CanRead("eng.ops") || scope.CanRead("secret.mna") {
		t.Error("stored read scope did not round-trip")
	}
	if !scope.TitleVisible("finance.q3") || scope.CanRead("finance.q3") {
		t.Error("finance must be title-visible but not readable")
	}
}

func TestWriteDeniedOutsideScope(t *testing.T) {
	eng, _, engKey, cleanup := setupSharedCorpus(t)
	defer cleanup()

	_, err := eng.WriteMemory(engKey, WriteInput{
		Title: "Sneaky", Content: "x", Taxonomy: "secret.mna",
	})
	ae, ok := err.(*ActionableError)
	if !ok || ae.Code != "taxonomy_forbidden" {
		t.Fatalf("write to secret = %v, want taxonomy_forbidden", err)
	}
	// Even a title-visible branch is not writable.
	_, err = eng.WriteMemory(engKey, WriteInput{
		Title: "Numbers", Content: "x", Taxonomy: "finance.q3",
	})
	if ae, ok := err.(*ActionableError); !ok || ae.Code != "taxonomy_forbidden" {
		t.Fatalf("write to finance = %v, want taxonomy_forbidden", err)
	}
}

func TestReadUnreadableLooksMissing(t *testing.T) {
	eng, adminKey, engKey, cleanup := setupSharedCorpus(t)
	defer cleanup()
	ids := seedShared(t, eng, adminKey)

	// Getting a secret memory by id is a 404-shaped miss, never "forbidden" —
	// otherwise the error would confirm the memory exists.
	_, err := eng.GetMemory(engKey, ids["secret"])
	ae, ok := err.(*ActionableError)
	if !ok || ae.Code != "not_found" {
		t.Fatalf("get secret = %v, want not_found (not forbidden)", err)
	}
	// The readable one is fine.
	if _, err := eng.GetMemory(engKey, ids["eng"]); err != nil {
		t.Errorf("get eng memory: %v", err)
	}
	// A title-visible finance memory: existence is known, but content read is
	// still denied via GetMemory, which is a content-level fetch.
	if _, err := eng.GetMemory(engKey, ids["finance"]); err == nil {
		t.Error("GetMemory on a title-only memory must not return content")
	}
}

func TestUpdateAndDeleteDeniedOutsideWriteScope(t *testing.T) {
	eng, adminKey, engKey, cleanup := setupSharedCorpus(t)
	defer cleanup()
	ids := seedShared(t, eng, adminKey)

	// Update a secret memory → looks missing (can't even read it).
	newBody := "hijacked"
	_, err := eng.UpdateMemory(engKey, ids["secret"], UpdateInput{Content: &newBody})
	if ae, ok := err.(*ActionableError); !ok || ae.Code != "not_found" {
		t.Fatalf("update secret = %v, want not_found", err)
	}
	// Delete likewise.
	if err := eng.DeleteMemory(engKey, ids["secret"], false); err != nil {
		if ae, ok := err.(*ActionableError); !ok || ae.Code != "not_found" {
			t.Fatalf("delete secret = %v, want not_found", err)
		}
	} else {
		t.Fatal("delete of an unreadable memory must fail")
	}

	// Moving a readable memory INTO secret is forbidden (write on destination).
	secretDest := "secret.mna"
	_, err = eng.UpdateMemory(engKey, ids["eng"], UpdateInput{Taxonomy: &secretDest})
	if ae, ok := err.(*ActionableError); !ok || ae.Code != "taxonomy_forbidden" {
		t.Fatalf("relocate into secret = %v, want taxonomy_forbidden", err)
	}
}

// Scenarios: spec/scope.md (SCOP-06)
func TestSearchAndCatalogScopedThroughEngine(t *testing.T) {
	eng, adminKey, engKey, cleanup := setupSharedCorpus(t)
	defer cleanup()
	seedShared(t, eng, adminKey)

	res, err := eng.Search(engKey, "body", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Results {
		if m.Taxonomy == "secret.mna" || m.Taxonomy == "finance.q3" {
			t.Errorf("search leaked %s", m.Taxonomy)
		}
	}

	cat, err := eng.Catalog(engKey, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range cat {
		if e.Path == "secret" {
			t.Error("catalog leaked the secret branch")
		}
	}

	// SCOP-06: admin (FullScope) still sees everything.
	adminRes, err := eng.Search(adminKey, "body", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminRes.Results) != 3 {
		t.Errorf("admin search = %d results, want 3", len(adminRes.Results))
	}
}

// TestWriterLinkOracleClosed: linking to a secret memory by its exact title must
// not confirm the memory exists via the resolved flag in the writer's feedback.
func TestWriterLinkOracleClosed(t *testing.T) {
	eng, adminKey, engKey, cleanup := setupSharedCorpus(t)
	defer cleanup()

	// Admin creates a secret memory titled "Acquisition".
	if _, err := eng.WriteMemory(adminKey, WriteInput{
		Title: "Acquisition", Content: "the terms", Taxonomy: "secret.mna",
	}); err != nil {
		t.Fatal(err)
	}

	// eng writes a readable memory linking that exact title. The link must come
	// back unresolved — otherwise resolved=true is an existence oracle.
	mem, err := eng.WriteMemory(engKey, WriteInput{
		Title: "Rumors", Content: "heard about [[Acquisition]]", Taxonomy: "eng.gossip",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mem.Links) != 1 {
		t.Fatalf("links = %d, want 1", len(mem.Links))
	}
	if mem.Links[0].Resolved {
		t.Error("link to a hidden memory reported resolved — existence oracle open")
	}

	// Admin linking the same title DOES see it resolve (full scope).
	adminMem, err := eng.WriteMemory(adminKey, WriteInput{
		Title: "Admin Ref", Content: "re [[Acquisition]]", Taxonomy: "eng.ops",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !adminMem.Links[0].Resolved {
		t.Error("admin link to the memory should resolve")
	}
}

// ── team management ─────────────────────────────────────────────────────────

func TestSoloUserIsOwnerOfTheirFile(t *testing.T) {
	eng, cleanup := setupEngine(t) // single identity, member-less file
	defer cleanup()

	me, err := eng.MyMembership(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if me.Role != db.RoleOwner {
		t.Errorf("solo role = %s, want owner", me.Role)
	}
	// And can therefore manage their own team-of-one.
	if !me.Role.CanManageMembers() {
		t.Error("solo owner must be able to manage members")
	}
}

func TestEditorCannotManageMembers(t *testing.T) {
	eng, _, engKey, cleanup := setupSharedCorpus(t)
	defer cleanup()

	_, err := eng.SetMember(engKey, "whoever", db.RoleViewer, nil, nil)
	if ae, ok := err.(*ActionableError); !ok || ae.Code != "forbidden" {
		t.Fatalf("editor SetMember = %v, want forbidden", err)
	}
	if err := eng.RemoveMember(engKey, "whoever"); err == nil {
		t.Error("editor RemoveMember must be forbidden")
	}
}

func TestAdminCannotTouchOwner(t *testing.T) {
	eng, adminKey, _, cleanup := setupSharedCorpus(t)
	defer cleanup()

	// Register a third identity and make them an Admin (by the Owner).
	adminID := registerIdentity(t, eng, "carol", "carol-key")
	if _, err := eng.SetMember(adminKey, adminID, db.RoleAdmin, nil, nil); err != nil {
		t.Fatal(err)
	}

	// The Owner's own identity id, discovered via membership listing.
	ownerID := ownerIDOf(t, eng, adminKey)

	// Admin cannot grant Owner...
	if _, err := eng.SetMember("carol-key", registerIdentity(t, eng, "dave", "dave-key"), db.RoleOwner, nil, nil); err == nil {
		t.Error("admin must not be able to grant the owner role")
	}
	// ...nor demote the existing Owner...
	if _, err := eng.SetMember("carol-key", ownerID, db.RoleEditor, nil, nil); err == nil {
		t.Error("admin must not be able to change an owner")
	}
	// ...nor remove the Owner.
	if err := eng.RemoveMember("carol-key", ownerID); err == nil {
		t.Error("admin must not be able to remove an owner")
	}
}

func TestCannotRemoveOrDemoteLastOwner(t *testing.T) {
	eng, adminKey, _, cleanup := setupSharedCorpus(t)
	defer cleanup()
	ownerID := ownerIDOf(t, eng, adminKey)

	// Owner demoting themselves while sole owner is refused.
	if _, err := eng.SetMember(adminKey, ownerID, db.RoleEditor, nil, nil); err == nil {
		t.Error("demoting the last owner must fail")
	}
	if err := eng.RemoveMember(adminKey, ownerID); err == nil {
		t.Error("removing the last owner must fail")
	}

	// With a second owner, the first may step down.
	otherID := registerIdentity(t, eng, "erin", "erin-key")
	if _, err := eng.SetMember(adminKey, otherID, db.RoleOwner, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.SetMember(adminKey, ownerID, db.RoleEditor, nil, nil); err != nil {
		t.Errorf("with two owners, one may be demoted: %v", err)
	}
}

func TestNonMemberOfTeamIsDenied(t *testing.T) {
	eng, _, _, cleanup := setupSharedCorpus(t)
	defer cleanup()

	// A registered identity pointed at the shared tenant but with no membership.
	registerIdentity(t, eng, "mallory", "mallory-key")
	// Every access must be refused — not defaulted to any role.
	if _, err := eng.Search("mallory-key", "anything", "", 0); err == nil {
		t.Error("a non-member must be denied access to the team corpus")
	} else if ae, ok := err.(*ActionableError); !ok || ae.Code != "not_a_member" {
		t.Errorf("denial code = %v, want not_a_member", err)
	}
}

func TestSetMemberRequiresRealIdentity(t *testing.T) {
	eng, adminKey, _, cleanup := setupSharedCorpus(t)
	defer cleanup()
	if _, err := eng.SetMember(adminKey, "ghost-id", db.RoleEditor, []string{"eng"}, nil); err == nil {
		t.Error("adding a phantom identity must fail")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

// registerIdentity creates an identity whose home is the shared tenant, so a
// membership row for it actually takes effect on resolve. It does NOT add the
// membership row — callers do that (or leave it absent to test denial).
func registerIdentity(t *testing.T, eng *Engine, name, key string) string {
	t.Helper()
	admin, err := eng.registry.Resolve("admin-key")
	if err != nil {
		t.Fatal(err)
	}
	id, err := eng.registry.Create(name, key, admin.MemoryPath, true) // same tenant path
	if err != nil {
		t.Fatal(err)
	}
	return id.ID
}

func ownerIDOf(t *testing.T, eng *Engine, ownerKey string) string {
	t.Helper()
	me, err := eng.MyMembership(ownerKey)
	if err != nil {
		t.Fatal(err)
	}
	return me.IdentityID
}
