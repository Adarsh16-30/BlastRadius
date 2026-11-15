package main

import "testing"

func TestLoadConfig(t *testing.T) {
	base := map[string]string{"DB_HOSTS": "toxiproxy:15432,toxiproxy:15433", "DB_USER": "app", "DB_PASSWORD": "x"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		return m
	}
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		hosts   int
		chaos   bool
	}{
		{"valid", base, false, 2, false},
		{"chaos on", with("CHAOS_ENABLED", "true"), false, 2, true},
		{"chaos junk", with("CHAOS_ENABLED", "maybe"), true, 0, false},
		{"no hosts", with("DB_HOSTS", ""), true, 0, false},
		{"host without port", with("DB_HOSTS", "toxiproxy"), true, 0, false},
		{"bad port", with("DB_HOSTS", "h:99999"), true, 0, false},
		{"no user", with("DB_USER", ""), true, 0, false},
		{"no password", with("DB_PASSWORD", ""), true, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := loadConfig(func(k string) string { return tc.env[k] })
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if err == nil && (len(c.DB.Hosts) != tc.hosts || c.ChaosEnabled != tc.chaos) {
				t.Fatalf("got %+v", c)
			}
		})
	}
}
