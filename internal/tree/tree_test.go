package tree

import "testing"

// The operations Purelymail's spec defined when this tree was generated. A
// regenerated tree that loses one should fail loudly.
var specOps = []string{
	"addDomain", "checkAccountCredit", "createAppPassword", "createRoutingRule",
	"createUser", "deleteAppPassword", "deleteDomain", "deletePasswordReset",
	"deleteRoutingRule", "deleteUser", "getOwnershipCode", "getUser",
	"listDomains", "listPasswordReset", "listRoutingRules", "listUser",
	"modifyUser", "updateDomainSettings", "upsertPasswordReset",
}

func TestEveryOperationIsReachable(t *testing.T) {
	tr, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got := tr.Operations()
	if len(got) != len(specOps) {
		t.Fatalf("have %d operations, want %d: %v", len(got), len(specOps), got)
	}
	for i := range specOps {
		if got[i] != specOps[i] {
			t.Errorf("operation %d = %s, want %s", i, got[i], specOps[i])
		}
	}
}

func TestOpsAreWellFormed(t *testing.T) {
	tr, _ := Load()
	if tr.API.BaseURL == "" || tr.API.AuthHeader != "Purelymail-Api-Token" {
		t.Errorf("api = %+v", tr.API)
	}
	seen := map[string]bool{}
	for _, r := range tr.Resources {
		for _, o := range r.Ops {
			key := r.Name + " " + o.Name
			if seen[key] {
				t.Errorf("duplicate command %s", key)
			}
			seen[key] = true
			if o.Method != "POST" {
				t.Errorf("%s: method %s", key, o.Method)
			}
			if wantDestructive := len(o.Operation) > 6 && o.Operation[:6] == "delete"; o.Destructive != wantDestructive && !o.Custom {
				t.Errorf("%s: destructive = %v", key, o.Destructive)
			}
			if o.Positional != "" {
				if _, ok := o.Param(o.Positional); !ok {
					t.Errorf("%s: positional %s is not a param", key, o.Positional)
				}
			}
			flags := map[string]bool{}
			for _, p := range o.Params {
				if p.Flag == "" || p.Kind == "" || flags[p.Flag] {
					t.Errorf("%s: bad param %+v", key, p)
				}
				flags[p.Flag] = true
			}
		}
	}
}

func TestSuggest(t *testing.T) {
	names := []string{"user", "domain", "routing"}
	if got := Suggest("domian", names, 3); len(got) == 0 || got[0] != "domain" {
		t.Errorf("Suggest(domian) = %v", got)
	}
	if got := Suggest("rout", names, 3); len(got) == 0 || got[0] != "routing" {
		t.Errorf("Suggest(rout) = %v", got)
	}
}
