package engine

import (
	"path/filepath"
	"testing"

	"github.com/Ucok23/rekam/internal/db"
)

// Scenarios: spec/search-taxonomy.md (SRCH-11, SRCH-12, SRCH-13, SRCH-14)

// SRCH-11
func TestTaxonomyTemplateDefaultsByKind(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	// A personal corpus falls back to the personal default, editable by its owner.
	personal, err := eng.TaxonomyTemplate(aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	if personal.Source != "default" || personal.Kind != "personal" {
		t.Errorf("personal template = %+v, want default/personal", personal)
	}
	if !personal.CanEdit {
		t.Error("solo owner should be able to edit the template")
	}
	if len(personal.Branches) == 0 || personal.Branches[0].Path != "work" {
		t.Errorf("personal default branches unexpected: %+v", personal.Branches)
	}

	// A team corpus falls back to the team default.
	team, _ := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	aliceTok := teamToken(eng, idOf(t, eng, aliceKey), team.ID)
	teamTmpl, err := eng.TaxonomyTemplate(aliceTok)
	if err != nil {
		t.Fatal(err)
	}
	if teamTmpl.Source != "default" || teamTmpl.Kind != "team" {
		t.Errorf("team template = %+v, want default/team", teamTmpl)
	}
	if teamTmpl.Branches[0].Path != "product" {
		t.Errorf("team default should lead with product: %+v", teamTmpl.Branches[0])
	}
}

// SRCH-12, SRCH-13
func TestTaxonomyTemplateSaveAndRevert(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()
	team, _ := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	aliceTok := teamToken(eng, idOf(t, eng, aliceKey), team.ID)

	custom := []db.TemplateBranch{
		{Path: "research", Description: "Experiments and findings"},
		{Path: "research.papers", Description: "Paper notes"},
	}
	saved, err := eng.SetTaxonomyTemplate(aliceTok, custom)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Source != "custom" || len(saved.Branches) != 2 || saved.Branches[0].Path != "research" {
		t.Fatalf("saved template = %+v, want custom research tree", saved)
	}
	// Sort follows slice order.
	if saved.Branches[0].Sort != 0 || saved.Branches[1].Sort != 1 {
		t.Errorf("sort not stamped from order: %+v", saved.Branches)
	}

	// Reading it back is still custom.
	got, _ := eng.TaxonomyTemplate(aliceTok)
	if got.Source != "custom" || got.Branches[1].Path != "research.papers" {
		t.Errorf("reload = %+v, want persisted custom", got)
	}

	// Clearing reverts to the shipped default.
	reverted, err := eng.SetTaxonomyTemplate(aliceTok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reverted.Source != "default" || reverted.Branches[0].Path != "product" {
		t.Errorf("cleared template = %+v, want team default", reverted)
	}
}

// SRCH-14
func TestTaxonomyTemplateEditGate(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	bob, _, err := eng.registry.CreateUser("Bob", "bob@acme.test", "password123",
		filepath.Join(dir, "bob.rekam"), true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	team, _ := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	bumpSeats(t, eng, team.ID)
	aliceTok := teamToken(eng, idOf(t, eng, aliceKey), team.ID)
	if _, err := eng.SetMember(aliceTok, bob.ID, db.RoleViewer, []string{"product"}, nil); err != nil {
		t.Fatal(err)
	}
	bobTok := teamToken(eng, bob.ID, team.ID)

	// A viewer can read the template but not edit it.
	got, err := eng.TaxonomyTemplate(bobTok)
	if err != nil {
		t.Fatal(err)
	}
	if got.CanEdit {
		t.Error("viewer must not be marked editable")
	}
	if _, err := eng.SetTaxonomyTemplate(bobTok, []db.TemplateBranch{{Path: "x"}}); err == nil {
		t.Error("viewer must not be able to set the template")
	}
}
