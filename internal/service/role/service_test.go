package role

import (
	"strings"
	"testing"
)

func TestNormalizeRoleNameUsesDatabaseCharacterLimit(t *testing.T) {
	name := "  Finance 🧾 "
	got, err := normalizeRoleName(name)
	if err != nil || got != "Finance 🧾" {
		t.Fatalf("normalizeRoleName(%q) = %q, %v; want trimmed value", name, got, err)
	}

	if _, err := normalizeRoleName(strings.Repeat("界", 100)); err != nil {
		t.Fatalf("100-character role name rejected: %v", err)
	}
	if _, err := normalizeRoleName(strings.Repeat("界", 101)); err != errRoleNameInvalid {
		t.Fatalf("101-character role name error = %v, want %v", err, errRoleNameInvalid)
	}
	if _, err := normalizeRoleName(" \t\n "); err != errRoleNameInvalid {
		t.Fatalf("whitespace-only role name error = %v, want %v", err, errRoleNameInvalid)
	}
}
