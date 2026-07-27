package cmd

import (
	"strings"
	"testing"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/console"
)

func TestResolveOrgRef(t *testing.T) {
	orgs := []orgRef{
		{ID: "org_1111", Name: "Acme Corp", Role: "owner"},
		{ID: "org_2222", Name: "Acme Labs", Role: "member"},
		{ID: "org_3333", Name: "Bravo", Role: "admin"},
	}

	cases := []struct {
		name    string
		arg     string
		wantID  string
		wantErr string
	}{
		{name: "exact id", arg: "org_2222", wantID: "org_2222"},
		{name: "exact name", arg: "Bravo", wantID: "org_3333"},
		{name: "exact name, different case", arg: "bRaVo", wantID: "org_3333"},
		{name: "unique prefix", arg: "bra", wantID: "org_3333"},
		{name: "unique prefix, different case", arg: "BRA", wantID: "org_3333"},
		{name: "exact name wins over being a prefix of another", arg: "Acme Corp", wantID: "org_1111"},
		{name: "ambiguous prefix", arg: "acme", wantErr: "ambiguous"},
		{name: "unknown", arg: "zeta", wantErr: "unknown organisation"},
		{name: "empty", arg: "  ", wantErr: "no organisation given"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveOrgRef(orgs, tc.arg)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveOrgRef: %v", err)
			}
			if got.ID != tc.wantID {
				t.Errorf("id = %q, want %q", got.ID, tc.wantID)
			}
		})
	}
}

func TestResolveOrgRefAmbiguousListsEveryMatchWithIDs(t *testing.T) {
	orgs := []orgRef{
		{ID: "org_1111", Name: "Acme Corp"},
		{ID: "org_2222", Name: "Acme Labs"},
	}
	_, err := resolveOrgRef(orgs, "acme")
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	for _, want := range []string{"org_1111", "org_2222", "Acme Corp", "Acme Labs"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestResolveOrgRefUnknownListsTheUsersOrgs(t *testing.T) {
	orgs := []orgRef{{ID: "org_1111", Name: "Acme Corp"}}
	_, err := resolveOrgRef(orgs, "nope")
	if err == nil {
		t.Fatal("expected an unknown-org error")
	}
	if !strings.Contains(err.Error(), "org_1111") || !strings.Contains(err.Error(), "Acme Corp") {
		t.Errorf("error does not list the known orgs: %v", err)
	}
}

func TestResolveOrgRefWithNoKnownOrgs(t *testing.T) {
	if _, err := resolveOrgRef(nil, "anything"); err == nil {
		t.Error("expected an error when nothing is known locally")
	}
}

func TestResolveOrgLocalUsesConfigOnly(t *testing.T) {
	cfg := &config.Config{Orgs: map[string]*config.OrgCreds{
		"org_1111": {Name: "Acme Corp"},
		"org_2222": {Name: "Bravo"},
	}}

	id, err := resolveOrgLocal(cfg, "bravo")
	if err != nil {
		t.Fatalf("resolveOrgLocal: %v", err)
	}
	if id != "org_2222" {
		t.Errorf("id = %q, want org_2222", id)
	}

	if _, err := resolveOrgLocal(cfg, "org_9999"); err == nil {
		t.Error("expected an error for an org the config has never seen")
	}
}

func TestOrgRefsFromConfigIsSorted(t *testing.T) {
	cfg := &config.Config{Orgs: map[string]*config.OrgCreds{
		"org_c": {Name: "Charlie"},
		"org_a": {Name: "alpha"},
		"org_b": {Name: "Bravo"},
	}}
	got := orgRefsFromConfig(cfg)
	want := []string{"org_a", "org_b", "org_c"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order = %+v, want %v", got, want)
		}
	}
}

func TestOrgRefsFromConsole(t *testing.T) {
	got := orgRefsFromConsole([]console.Org{{ID: "org_a", Name: "Alpha", Role: "owner"}})
	if len(got) != 1 || got[0].ID != "org_a" || got[0].Role != "owner" {
		t.Errorf("refs = %+v", got)
	}
}
