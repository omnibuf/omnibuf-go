package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseBenchFileAveragesAllMetricsAndRuntimeVersion(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "benchmarks.txt")
	sample := strings.Join([]string{
		"goos: darwin",
		"goarch: arm64",
		"cpu: Apple M1 Pro",
		"BenchmarkPlugin_Marshal_PBStruct_ReuseBuf-8\t1000000\t120.0 ns/op\t300 B/op\t2 allocs/op",
		"BenchmarkPlugin_Marshal_PBStruct_ReuseBuf-8\t1000000\t130.0 ns/op\t500 B/op\t6 allocs/op",
	}, "\n")
	if err := os.WriteFile(inPath, []byte(sample), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	goos, goarch, cpu, metrics, err := parseBenchFile(inPath)
	if err != nil {
		t.Fatalf("parseBenchFile: %v", err)
	}
	report := buildReport(goos, goarch, cpu, metrics)

	wantRuntime := "Go " + strings.TrimPrefix(runtime.Version(), "go")
	if report.Runtime != wantRuntime {
		t.Fatalf("Runtime = %q, want %q", report.Runtime, wantRuntime)
	}
	if len(report.Categories) == 0 || len(report.Categories[0].Results) != 1 {
		t.Fatalf("unexpected encode results: %+v", report.Categories)
	}
	got := report.Categories[0].Results[0]
	if got.NsPerOp != 125 {
		t.Fatalf("NsPerOp = %v, want 125", got.NsPerOp)
	}
	if got.BytesPerOp != 400 {
		t.Fatalf("BytesPerOp = %d, want 400 (averaged across runs)", got.BytesPerOp)
	}
	if got.AllocsPerOp != 4 {
		t.Fatalf("AllocsPerOp = %d, want 4 (averaged across runs)", got.AllocsPerOp)
	}

	wantCategories := []struct {
		id    string
		title string
	}{
		{"encode", "Full Message Encode Speed"},
		{"decode", "Full Message Decode Speed"},
		{"lookup_city", "Read a Single Nested Field"},
		{"mutate_sequence", "Selective Single-Field Mutation"},
		{"encode_nested_shift", "Large Embedded Message Encode"},
		{"mutate_embedded_grow", "Embedded Message Growth"},
	}
	if len(report.Categories) != len(wantCategories) {
		t.Fatalf("len(Categories) = %d, want %d", len(report.Categories), len(wantCategories))
	}
	for i, want := range wantCategories {
		cat := report.Categories[i]
		if cat.ID != want.id || cat.Title != want.title {
			t.Fatalf("Categories[%d] = (%q, %q), want (%q, %q)", i, cat.ID, cat.Title, want.id, want.title)
		}
		if strings.ContainsAny(cat.Title, "()") {
			t.Fatalf("Categories[%d].Title %q should not contain parentheticals", i, cat.Title)
		}
	}
}
