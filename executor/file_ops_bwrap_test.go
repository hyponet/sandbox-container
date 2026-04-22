package executor

import "testing"

func TestParseStat_UsesBaseName(t *testing.T) {
	info, err := parseStat("/data/agents/a1/sessions/s1/file.txt|12|81a4|1710000000|regular file")
	if err != nil {
		t.Fatalf("parseStat returned error: %v", err)
	}
	if info.Name != "file.txt" {
		t.Fatalf("parseStat Name = %q, want %q", info.Name, "file.txt")
	}
}

func TestParseFindOutput_UsesBaseName(t *testing.T) {
	entries, err := parseFindOutput("/data/agents/a1/sessions/s1/.hidden.txt|4|644|1710000000.0|f\n")
	if err != nil {
		t.Fatalf("parseFindOutput returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("parseFindOutput len = %d, want 1", len(entries))
	}
	if entries[0].Name != ".hidden.txt" {
		t.Fatalf("parseFindOutput Name = %q, want %q", entries[0].Name, ".hidden.txt")
	}
}

func TestParseWalkOutput_PreservesPathAndUsesBaseName(t *testing.T) {
	entries, err := parseWalkOutput("/data/agents/a1/sessions/s1/sub/code.go|32|644|1710000000.0|f\n")
	if err != nil {
		t.Fatalf("parseWalkOutput returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("parseWalkOutput len = %d, want 1", len(entries))
	}
	if entries[0].Path != "/data/agents/a1/sessions/s1/sub/code.go" {
		t.Fatalf("parseWalkOutput Path = %q", entries[0].Path)
	}
	if entries[0].Info.Name != "code.go" {
		t.Fatalf("parseWalkOutput Name = %q, want %q", entries[0].Info.Name, "code.go")
	}
}
