package main

import (
	"bytes"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no args is usage error", nil, 2},
		{"subcommand is not implemented yet", []string{"run"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if got := run(&buf, tt.args); got != tt.want {
				t.Fatalf("run(%v) = %d, want %d", tt.args, got, tt.want)
			}
			if buf.Len() == 0 {
				t.Fatal("expected output")
			}
		})
	}
}
