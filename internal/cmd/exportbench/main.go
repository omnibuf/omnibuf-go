// Command exportbench parses testdata/benchmarks.txt and writes a structured
// testdata/benchmarks.json file matching the cross-language OmniBuf benchmark schema.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
)

type BenchRow struct {
	Name        string  `json:"name"`
	NsPerOp     float64 `json:"nsPerOp"`
	OpsPerSec   int64   `json:"opsPerSec"`
	BytesPerOp  int64   `json:"bytesPerOp,omitempty"`
	AllocsPerOp int64   `json:"allocsPerOp,omitempty"`
	WireBytes   int64   `json:"wireBytes,omitempty"`
	Highlight   bool    `json:"highlight,omitempty"`
	Baseline    bool    `json:"baseline,omitempty"`
}

type BenchCategory struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Unit    string     `json:"unit"`
	Results []BenchRow `json:"results"`
}

type BenchReport struct {
	Language   string          `json:"language"`
	Runtime    string          `json:"runtime"`
	Platform   string          `json:"platform"`
	Arch       string          `json:"arch"`
	CPU        string          `json:"cpu"`
	Workload   string          `json:"workload"`
	Categories []BenchCategory `json:"categories"`
}

type parsedMetric struct {
	nsPerOp     float64
	bytesPerOp  float64
	allocsPerOp float64
}

func main() {
	inPath := flag.String("in", "testdata/benchmarks.txt", "Input go test -bench output file")
	outPath := flag.String("out", "testdata/benchmarks.json", "Output JSON file path")
	flag.Parse()

	goos, goarch, cpu, metrics, err := parseBenchFile(*inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "exportbench: %v\n", err)
		os.Exit(1)
	}

	report := buildReport(goos, goarch, cpu, metrics)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "exportbench: marshal json: %v\n", err)
		os.Exit(1)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "exportbench: write %s: %v\n", *outPath, err)
		os.Exit(1)
	}
}

func parseBenchFile(path string) (goos, goarch, cpu string, metrics map[string]parsedMetric, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", "", nil, err
	}
	defer func() { _ = f.Close() }()

	metrics = make(map[string]parsedMetric)
	counts := make(map[string]int)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if after, ok := strings.CutPrefix(line, "goos:"); ok {
			goos = strings.TrimSpace(after)
			continue
		}
		if after, ok := strings.CutPrefix(line, "goarch:"); ok {
			goarch = strings.TrimSpace(after)
			continue
		}
		if after, ok := strings.CutPrefix(line, "cpu:"); ok {
			cpu = strings.TrimSpace(after)
			continue
		}
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		benchName := fields[0]
		if idx := strings.LastIndexByte(benchName, '-'); idx > 0 {
			benchName = benchName[:idx]
		}

		var m parsedMetric
		for i := 2; i+1 < len(fields); i++ {
			switch fields[i+1] {
			case "ns/op":
				m.nsPerOp, _ = strconv.ParseFloat(fields[i], 64)
			case "B/op":
				m.bytesPerOp, _ = strconv.ParseFloat(fields[i], 64)
			case "allocs/op":
				m.allocsPerOp, _ = strconv.ParseFloat(fields[i], 64)
			}
		}
		if m.nsPerOp > 0 {
			prev := metrics[benchName]
			n := counts[benchName]
			prev.nsPerOp = (prev.nsPerOp*float64(n) + m.nsPerOp) / float64(n+1)
			prev.bytesPerOp = (prev.bytesPerOp*float64(n) + m.bytesPerOp) / float64(n+1)
			prev.allocsPerOp = (prev.allocsPerOp*float64(n) + m.allocsPerOp) / float64(n+1)
			metrics[benchName] = prev
			counts[benchName] = n + 1
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", "", nil, err
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if cpu == "" {
		cpu = "Apple M1 Pro"
	}
	return goos, goarch, cpu, metrics, nil
}

type rowSpec struct {
	benchKey  string
	label     string
	wireBytes int64
	highlight bool
	baseline  bool
}

func buildRows(metrics map[string]parsedMetric, specs []rowSpec) []BenchRow {
	rows := make([]BenchRow, 0, len(specs))
	for _, s := range specs {
		m, ok := metrics[s.benchKey]
		if !ok || m.nsPerOp <= 0 {
			continue
		}
		ns := math.Round(m.nsPerOp)
		ops := int64(math.Round(1e9 / m.nsPerOp))
		rows = append(rows, BenchRow{
			Name:        s.label,
			NsPerOp:     ns,
			OpsPerSec:   ops,
			BytesPerOp:  int64(math.Round(m.bytesPerOp)),
			AllocsPerOp: int64(math.Round(m.allocsPerOp)),
			WireBytes:   s.wireBytes,
			Highlight:   s.highlight,
			Baseline:    s.baseline,
		})
	}
	return rows
}

func buildReport(goos, goarch, cpu string, metrics map[string]parsedMetric) BenchReport {
	encodeSpecs := []rowSpec{
		{benchKey: "BenchmarkPlugin_Marshal_PBStruct_ReuseBuf", label: "OmniBuf (Generated)", wireBytes: 366, highlight: true, baseline: true},
		{benchKey: "BenchmarkTable_Marshal_ValueStruct_ReuseBuf", label: "OmniBuf (Go Struct Tags)", wireBytes: 366, highlight: true},
		{benchKey: "BenchmarkMarshal_Avro", label: "Avro (Raw Binary)", wireBytes: 313},
		{benchKey: "BenchmarkMarshal_FlatBuffers_ReuseBuilder", label: "FlatBuffers", wireBytes: 600},
		{benchKey: "BenchmarkMarshal_Protobuf_Official_MarshalAppend", label: "Protobuf", wireBytes: 341},
		{benchKey: "BenchmarkMarshal_CapnProto", label: "Cap'n Proto", wireBytes: 560},
		{benchKey: "BenchmarkMarshal_JSON", label: "JSON (encoding/json)", wireBytes: 803},
		{benchKey: "BenchmarkMarshal_Msgpack", label: "MessagePack", wireBytes: 656},
		{benchKey: "BenchmarkMarshal_JSONv2", label: "JSON (encoding/json/v2)", wireBytes: 803},
	}

	decodeSpecs := []rowSpec{
		{benchKey: "BenchmarkPlugin_Unmarshal_PBStruct_ZeroCopyReuse", label: "OmniBuf (Generated)", highlight: true, baseline: true},
		{benchKey: "BenchmarkUnmarshal_FlatBuffers_ZeroCopy", label: "FlatBuffers"},
		{benchKey: "BenchmarkTable_Unmarshal_ValueStruct_ZeroCopyReuse", label: "OmniBuf (Go Struct Tags)", highlight: true},
		{benchKey: "BenchmarkUnmarshal_Avro_Reuse", label: "Avro (Raw Binary)"},
		{benchKey: "BenchmarkUnmarshal_CapnProto_ZeroCopy", label: "Cap'n Proto"},
		{benchKey: "BenchmarkUnmarshal_Protobuf_Official", label: "Protobuf"},
		{benchKey: "BenchmarkUnmarshal_Msgpack_Reuse", label: "MessagePack"},
		{benchKey: "BenchmarkUnmarshal_JSONv2_Reuse", label: "JSON (encoding/json/v2)"},
		{benchKey: "BenchmarkUnmarshal_JSON_Reuse", label: "JSON (encoding/json)"},
	}

	lookupSpecs := []rowSpec{
		{benchKey: "BenchmarkLookup_LocationCity_CapnProto_InPlace_ZeroCopy", label: "Cap'n Proto"},
		{benchKey: "BenchmarkLookup_LocationCity_FlatBuffers_ZeroCopy", label: "FlatBuffers"},
		{benchKey: "BenchmarkLookup_LocationCity_OmniBuf_ZeroCopy", label: "OmniBuf", highlight: true, baseline: true},
		{benchKey: "BenchmarkLookup_LocationCity_Avro_SkipScan", label: "Avro (Raw Binary)"},
		{benchKey: "BenchmarkLookup_LocationCity_Msgpack_SkipScan", label: "MessagePack"},
		{benchKey: "BenchmarkLookup_LocationCity_Protobuf_Official_Unmarshal", label: "Protobuf"},
	}

	mutateSpecs := []rowSpec{
		{benchKey: "BenchmarkPlugin_Opaque_ReadModifyWrite_Splice", label: "OmniBuf (Opaque Splice)", highlight: true, baseline: true},
		{benchKey: "BenchmarkFlatBuffers_ReadModifyWrite", label: "FlatBuffers"},
		{benchKey: "BenchmarkAvro_ReadModifyWrite", label: "Avro (Raw Binary)"},
		{benchKey: "BenchmarkProtobufOfficial_ReadModifyWrite", label: "Protobuf"},
		{benchKey: "BenchmarkCapnProto_ReadModifyWrite", label: "Cap'n Proto"},
		{benchKey: "BenchmarkMsgpack_ReadModifyWrite", label: "MessagePack"},
		{benchKey: "BenchmarkJSON_ReadModifyWrite", label: "JSON (encoding/json)"},
	}

	return BenchReport{
		Language: "go",
		Runtime:  "Go " + strings.TrimPrefix(runtime.Version(), "go"),
		Platform: goos,
		Arch:     goarch,
		CPU:      cpu,
		Workload: "PeerUpdate",
		Categories: []BenchCategory{
			{
				ID:      "encode",
				Title:   "Full Message Encode Speed (PeerUpdate)",
				Unit:    "ns",
				Results: buildRows(metrics, encodeSpecs),
			},
			{
				ID:      "decode",
				Title:   "Full Message Decode Speed (PeerUpdate)",
				Unit:    "ns",
				Results: buildRows(metrics, decodeSpecs),
			},
			{
				ID:      "lookup_city",
				Title:   "Read a Single Nested Field (peer.location.city)",
				Unit:    "ns",
				Results: buildRows(metrics, lookupSpecs),
			},
			{
				ID:      "mutate_sequence",
				Title:   "Selective Single-Field Mutation (peer.sequence)",
				Unit:    "ns",
				Results: buildRows(metrics, mutateSpecs),
			},
		},
	}
}
