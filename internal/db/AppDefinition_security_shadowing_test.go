package db

import (
	"strings"
	"testing"
)

// rules builds a rule with the defaults these tests care about, so each case
// only states what it is actually about.
func rule(name string, order int, effect, operation, field, role, condition string) AppDefinitionSecurityRule {
	return AppDefinitionSecurityRule{
		Name:      name,
		Order:     order,
		Effect:    effect,
		Operation: operation,
		Table:     "dw_story",
		Field:     field,
		Role:      role,
		Condition: condition,
	}
}

func TestShadowedDenyIsRejected(t *testing.T) {
	// The bug this guard exists for: a plain allow first, then a deny that
	// reads as a restriction but never runs.
	rules := []AppDefinitionSecurityRule{
		rule("reader_can_read", 10, "allow", "R", "", "reader", ""),
		rule("hide_closed", 20, "deny", "R", "", "reader", `state == "closed"`),
	}

	err := validateSecurityRuleShadowing(rules, "security for table \"dw_story\"", "dw_story")
	if err == nil {
		t.Fatal("expected the shadowed deny to be rejected")
	}
	message := err.Error()
	for _, want := range []string{"hide_closed", "reader_can_read", "silently inert"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not mention %q", message, want)
		}
	}
}

func TestShadowedAllowIsRejected(t *testing.T) {
	// A dead allow is less dangerous but equally invisible.
	rules := []AppDefinitionSecurityRule{
		rule("reader_can_read", 10, "allow", "R", "", "reader", ""),
		rule("reader_can_read_again", 20, "allow", "R", "", "reader", `state == "open"`),
	}

	err := validateSecurityRuleShadowing(rules, "security", "dw_story")
	if err == nil {
		t.Fatal("expected the shadowed allow to be rejected")
	}
	if !strings.Contains(err.Error(), "can never apply") {
		t.Fatalf("error = %q, want a 'can never apply' message", err)
	}
}

func TestIdenticalConditionsWithDifferentEffectsAreRejected(t *testing.T) {
	// Two rules that always match together: the second is unreachable, and
	// the pair reads as contradictory.
	rules := []AppDefinitionSecurityRule{
		rule("grant_open", 10, "allow", "U", "", "editor", `state == "draft"`),
		rule("block_open", 20, "deny", "U", "", "editor", `state == "draft"`),
	}

	err := validateSecurityRuleShadowing(rules, "security", "dw_story")
	if err == nil {
		t.Fatal("expected the unreachable rule to be rejected")
	}
	if !strings.Contains(err.Error(), "same condition") {
		t.Fatalf("error = %q, want it to cite the shared condition", err)
	}
}

func TestWhitelistPatternIsAllowed(t *testing.T) {
	// Conditional allow, then unconditional deny: the documented way to
	// restrict a role to a subset of records. The deny must stay valid.
	rules := []AppDefinitionSecurityRule{
		rule("open_records_readable", 10, "allow", "R", "", "reader", `state == "open"`),
		rule("everything_else_denied", 20, "deny", "R", "", "reader", ""),
	}

	if err := validateSecurityRuleShadowing(rules, "security", "dw_story"); err != nil {
		t.Fatalf("whitelist pattern must validate, got %v", err)
	}
}

func TestDenyFirstThenAllowIsAllowed(t *testing.T) {
	// Deny placed first: it decides for its condition, the allow takes the
	// rest. Nothing is unreachable.
	rules := []AppDefinitionSecurityRule{
		rule("hide_closed", 10, "deny", "R", "", "reader", `state == "closed"`),
		rule("reader_can_read", 20, "allow", "R", "", "reader", ""),
	}

	if err := validateSecurityRuleShadowing(rules, "security", "dw_story"); err != nil {
		t.Fatalf("deny-first pattern must validate, got %v", err)
	}
}

func TestDifferentRolesAreNotShadowed(t *testing.T) {
	// The rules compete for different users, so neither can shadow the other.
	rules := []AppDefinitionSecurityRule{
		rule("reader_can_read", 10, "allow", "R", "", "reader", ""),
		rule("manager_denied", 20, "deny", "R", "", "manager", `state == "closed"`),
	}

	if err := validateSecurityRuleShadowing(rules, "security", "dw_story"); err != nil {
		t.Fatalf("rules for different roles must validate, got %v", err)
	}
}

func TestDifferentOperationsAreNotShadowed(t *testing.T) {
	rules := []AppDefinitionSecurityRule{
		rule("reader_can_read", 10, "allow", "R", "", "reader", ""),
		rule("reader_cannot_update", 20, "deny", "U", "", "reader", ""),
	}

	if err := validateSecurityRuleShadowing(rules, "security", "dw_story"); err != nil {
		t.Fatalf("rules for different operations must validate, got %v", err)
	}
}

func TestDifferentFieldsAreNotShadowed(t *testing.T) {
	// Record-level and field-level rules are separate scopes in the
	// evaluator, so they never compete.
	rules := []AppDefinitionSecurityRule{
		rule("reader_can_read", 10, "allow", "R", "", "reader", ""),
		rule("hide_state", 20, "deny", "R", "state", "reader", ""),
	}

	if err := validateSecurityRuleShadowing(rules, "security", "dw_story"); err != nil {
		t.Fatalf("rules for different fields must validate, got %v", err)
	}
}

func TestDifferentTablesAreNotShadowed(t *testing.T) {
	// Page security can carry rules for several tables; they are separate
	// scopes.
	first := rule("reader_can_read", 10, "allow", "R", "", "reader", "")
	first.Table = "dw_story"
	second := rule("hide_closed", 20, "deny", "R", "", "reader", `state == "closed"`)
	second.Table = "dw_chapter"

	if err := validateSecurityRuleShadowing([]AppDefinitionSecurityRule{first, second}, "security", ""); err != nil {
		t.Fatalf("rules for different tables must validate, got %v", err)
	}
}

func TestShadowingRespectsOrderNotSlicePosition(t *testing.T) {
	// The deny is listed first in the slice but ordered later: evaluation
	// sorts by order, so it is still dead.
	rules := []AppDefinitionSecurityRule{
		rule("hide_closed", 20, "deny", "R", "", "reader", `state == "closed"`),
		rule("reader_can_read", 10, "allow", "R", "", "reader", ""),
	}

	err := validateSecurityRuleShadowing(rules, "security", "dw_story")
	if err == nil {
		t.Fatal("expected shadowing to be detected via order, not slice position")
	}
	if !strings.Contains(err.Error(), "hide_closed") {
		t.Fatalf("error = %q, want it to name hide_closed", err)
	}
}

func TestRuleMatchingItsOwnTableIsInScope(t *testing.T) {
	// expectedTable supplies the table when the rule omits it; a rule that
	// names its own table must land in the same scope.
	implicit := rule("reader_can_read", 10, "allow", "R", "", "reader", "")
	implicit.Table = ""
	explicit := rule("hide_closed", 20, "deny", "R", "", "reader", "")
	explicit.Table = "dw_story"

	err := validateSecurityRuleShadowing([]AppDefinitionSecurityRule{implicit, explicit}, "security", "dw_story")
	if err == nil {
		t.Fatal("expected implicit and explicit table rules to share a scope")
	}
}

func TestSingleRuleIsNeverShadowed(t *testing.T) {
	rules := []AppDefinitionSecurityRule{rule("only", 10, "deny", "R", "", "reader", "")}
	if err := validateSecurityRuleShadowing(rules, "security", "dw_story"); err != nil {
		t.Fatalf("single rule must validate, got %v", err)
	}
	if err := validateSecurityRuleShadowing(nil, "security", "dw_story"); err != nil {
		t.Fatalf("no rules must validate, got %v", err)
	}
}
