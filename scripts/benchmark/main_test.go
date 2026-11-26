package main

import "testing"

// TestValidate memastikan benchmark menolak database lain dan parameter di luar batas.
func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		action, database string
		rows             int64
		valid            bool
	}{
		{"generate", databaseName, 1, true},
		{"reset", databaseName, 1_000_000, true},
		{"run", databaseName, 100_000_000, true},
		{"generate", "platform", 1_000_000, false},
		{"reset", "target_db", 1_000_000, false},
		{"generate", databaseName, 0, false},
		{"generate", databaseName, 100_000_001, false},
		{"unknown", databaseName, 1, false},
	} {
		if err := validate(tc.action, tc.rows, tc.database); (err == nil) != tc.valid {
			t.Errorf("validate(%q, %d, %q) = %v", tc.action, tc.rows, tc.database, err)
		}
	}
}
