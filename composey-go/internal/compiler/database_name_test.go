package compiler

import (
	"strings"
	"testing"
)

// --- test_database_name.py: derivation and sanitization --------------------

func TestDatabaseNameDefaultAvoidsReservedWord(t *testing.T) {
	t.Parallel()
	// The bug that broke acceptance: the bare service name "db" collided
	// with the engine's own reserved word.
	result := DatabaseName("doctor", "db", map[string]string{})
	if result != "doctor_db" {
		t.Errorf("DatabaseName(doctor, db, {}) = %q, want doctor_db", result)
	}
}

func TestDatabaseNameStatedIsHonouredVerbatim(t *testing.T) {
	t.Parallel()
	result := DatabaseName("shop", "db", map[string]string{"POSTGRES_DB": "orders"})
	if result != "orders" {
		t.Errorf("DatabaseName with POSTGRES_DB=orders = %q, want orders", result)
	}
}

func TestDatabaseNameOnlyReferencedIsNotUsed(t *testing.T) {
	t.Parallel()
	// An unresolved ${POSTGRES_DB} is absent from the environment dict
	// entirely by this point (declaredEnvironment strips it out) — so this
	// tests the fallback behaviour when the key is simply not present.
	result := DatabaseName("shop", "db", map[string]string{})
	if result != "shop_db" {
		t.Errorf("DatabaseName with no env = %q, want shop_db", result)
	}
}

func TestDatabaseNameSanitization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw      string
		expected string
	}{
		{"my-app", "my_app"},
		{"My_App", "my_app"},
		{"2fast", "fast"},
		{"_", "app"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			result := SanitizeDatabaseName(tc.raw)
			if result != tc.expected {
				t.Errorf("SanitizeDatabaseName(%q) = %q, want %q", tc.raw, result, tc.expected)
			}
		})
	}
}

func TestDatabaseNameLengthCap(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 80)
	result := SanitizeDatabaseName(long)
	if len(result) > 63 {
		t.Errorf("SanitizeDatabaseName length = %d, want <= 63", len(result))
	}
}
