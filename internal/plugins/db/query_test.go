package dbapp

import "testing"

func TestSQLGuardReason(t *testing.T) {
	cases := []struct {
		sql  string
		want bool
	}{
		{"SELECT * FROM users", true},
		{"select * from users limit 10", false},
		{"SELECT count(*) FROM users", true},
		{"SELECT 1", false},
		{"SELECT 'where' FROM users", true},
		{"-- note\nSELECT * FROM users WHERE id = 1", false},
		{"/* note */ SELECT * FROM users", true},
		{"WITH c AS (SELECT * FROM users WHERE id > 1) SELECT * FROM c", false},
		{"UPDATE users SET name = 'x'", true},
		{"DELETE FROM users WHERE id = 1", false},
		{"INSERT INTO users (name) VALUES ('x')", false},
	}
	for _, tc := range cases {
		got := sqlGuardReason(tc.sql) != ""
		if got != tc.want {
			t.Errorf("sqlGuardReason(%q) needs confirm = %v, want %v", tc.sql, got, tc.want)
		}
	}
}
