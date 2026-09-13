package main

import "testing"

func TestExtractIntFlag(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    int
		wantSet []string
		wantErr bool
	}{
		{
			name:    "tool then equals form (the exact case that was broken)",
			args:    []string{"ollama", "--keep=5"},
			want:    5,
			wantSet: []string{"ollama"},
		},
		{
			name:    "equals form then tool",
			args:    []string{"--keep=5", "ollama"},
			want:    5,
			wantSet: []string{"ollama"},
		},
		{
			name:    "space form",
			args:    []string{"ollama", "--keep", "7"},
			want:    7,
			wantSet: []string{"ollama"},
		},
		{
			name:    "no flag at all uses default",
			args:    []string{"ollama"},
			want:    2,
			wantSet: []string{"ollama"},
		},
		{
			name:    "no tool, just the flag",
			args:    []string{"--keep=3"},
			want:    3,
			wantSet: []string{},
		},
		{
			name:    "invalid value",
			args:    []string{"ollama", "--keep=nope"},
			wantErr: true,
		},
		{
			name:    "space form missing value",
			args:    []string{"ollama", "--keep"},
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rest, err := extractIntFlag(c.args, "keep", 2)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got value=%d rest=%v", got, rest)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("value = %d, want %d", got, c.want)
			}
			if len(rest) != len(c.wantSet) {
				t.Errorf("rest = %v, want %v", rest, c.wantSet)
			} else {
				for i := range rest {
					if rest[i] != c.wantSet[i] {
						t.Errorf("rest = %v, want %v", rest, c.wantSet)
						break
					}
				}
			}
		})
	}
}
