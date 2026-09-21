package engine

import (
	"fmt"

	"github.com/Ucok23/rekam/internal/db"
)

// A taxonomy template is a corpus's declared classification scaffold: the
// branches a human or agent is expected to file into, each with a short "what
// goes here". It exists to make an empty corpus approachable — the picker can
// suggest branches before any record exists, and an agent gets a fixed
// vocabulary to classify into instead of inventing paths per session.
//
// A corpus with no saved template falls back to one of the shipped defaults
// below, chosen by kind (team vs personal), WITHOUT persisting it. The first
// edit copies the effective template forward into tenant rows, which then take
// over. Templates never create records — they only suggest structure.

// defaultPersonalTemplate is the starter scaffold for a personal corpus: the
// broad areas of one person's life and work.
var defaultPersonalTemplate = []db.TemplateBranch{
	{Path: "work", Description: "Professional output, tasks, and job context"},
	{Path: "work.projects", Description: "Active projects — goals, status, notes"},
	{Path: "work.meetings", Description: "Meeting notes and action items"},
	{Path: "personal", Description: "Life admin and day-to-day"},
	{Path: "people", Description: "Relationships — who's who, context on people you know"},
	{Path: "finance", Description: "Accounts, budgets, subscriptions, taxes"},
	{Path: "health", Description: "Medical records, fitness, habits"},
	{Path: "learning", Description: "Notes from reading, courses, and ideas"},
	{Path: "learning.books", Description: "Book notes and highlights"},
	{Path: "home", Description: "Household, maintenance, purchases"},
}

// defaultTeamTemplate is the starter scaffold for a shared team corpus: the
// functions and artifacts a team accumulates.
var defaultTeamTemplate = []db.TemplateBranch{
	{Path: "product", Description: "Specs, roadmap, and product decisions"},
	{Path: "product.specs", Description: "Feature and product specifications"},
	{Path: "product.decisions", Description: "Decision records — what was chosen and why"},
	{Path: "engineering", Description: "Architecture, systems, and technical notes"},
	{Path: "engineering.runbooks", Description: "Operational how-tos and playbooks"},
	{Path: "engineering.incidents", Description: "Incident write-ups and postmortems"},
	{Path: "design", Description: "Design system, patterns, and research"},
	{Path: "ops", Description: "Process, vendors, tooling, and logistics"},
	{Path: "people", Description: "Team, roles, and onboarding"},
	{Path: "meetings", Description: "Shared meeting notes and action items"},
	{Path: "knowledge", Description: "Reference material and how-tos"},
}

// defaultTemplateFor returns the shipped default for a corpus kind. teamID is
// non-empty for a registered team corpus; everything else is personal.
func defaultTemplateFor(teamID string) []db.TemplateBranch {
	if teamID != "" {
		return withSort(defaultTeamTemplate)
	}
	return withSort(defaultPersonalTemplate)
}

// withSort stamps declaration order onto Sort so shipped defaults render in the
// order written, matching how saved templates carry their sort.
func withSort(branches []db.TemplateBranch) []db.TemplateBranch {
	out := make([]db.TemplateBranch, len(branches))
	for i, b := range branches {
		b.Sort = i
		out[i] = b
	}
	return out
}

// TaxonomyTemplateResult is the effective template for a corpus plus the
// provenance a caller needs to render it: whether it's the shipped default or a
// saved custom one, which kind it is, and whether this caller may edit it.
type TaxonomyTemplateResult struct {
	Branches []db.TemplateBranch `json:"branches"`
	Source   string              `json:"source"` // "custom" (saved) | "default" (shipped)
	Kind     string              `json:"kind"`   // "team" | "personal"
	CanEdit  bool                `json:"can_edit"`
}

// TaxonomyTemplate returns the corpus's effective taxonomy template: its saved
// branches, or the shipped default for its kind when none is saved. Any member
// may read it; the result reports whether this caller may edit.
func (e *Engine) TaxonomyTemplate(apiKey string) (*TaxonomyTemplateResult, error) {
	identity, mdb, _, teamID, _, release, err := e.resolveTenant(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	caller, err := e.memberOf(mdb, identity.ID)
	if err != nil {
		return nil, err
	}
	return e.taxonomyTemplateFor(mdb, caller, teamID)
}

// SetTaxonomyTemplate replaces the corpus's taxonomy template. Requires a
// management role (Owner or Admin — the roles that own taxonomy policy). An
// empty slice clears the saved template, reverting to the shipped default.
func (e *Engine) SetTaxonomyTemplate(apiKey string, branches []db.TemplateBranch) (*TaxonomyTemplateResult, error) {
	identity, mdb, _, teamID, path, release, err := e.resolveTenant(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	caller, err := e.memberOf(mdb, identity.ID)
	if err != nil {
		return nil, err
	}
	if !caller.Role.CanManageMembers() {
		return nil, &ActionableError{Code: "forbidden", Message: "your role cannot edit the taxonomy template"}
	}
	// resolveTenant was called with requireWrite=false (CanManageMembers is
	// the real gate here, not AllowWrite), so its own lease check didn't run —
	// this still mutates the tenant file, so check explicitly.
	if err := e.requireTenantPrimary(path); err != nil {
		return nil, err
	}
	if err := mdb.SetTaxonomyTemplate(branches); err != nil {
		return nil, fmt.Errorf("set taxonomy template: %w", err)
	}
	return e.taxonomyTemplateFor(mdb, caller, teamID)
}

// taxonomyTemplateFor builds the result from an already-resolved tenant, so a
// write can return the fresh effective template without a second resolve.
func (e *Engine) taxonomyTemplateFor(mdb *db.MemoryDB, caller *db.Member, teamID string) (*TaxonomyTemplateResult, error) {
	kind := "personal"
	if teamID != "" {
		kind = "team"
	}
	saved, err := mdb.TaxonomyTemplate()
	if err != nil {
		return nil, err
	}
	res := &TaxonomyTemplateResult{Kind: kind, CanEdit: caller.Role.CanManageMembers()}
	if len(saved) > 0 {
		res.Branches, res.Source = saved, "custom"
	} else {
		res.Branches, res.Source = defaultTemplateFor(teamID), "default"
	}
	return res, nil
}
