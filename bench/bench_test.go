package bench

import (
	"testing"
)

func TestParseOutput_Basic(t *testing.T) {
	data := []byte(`goos: linux
goarch: amd64
BenchmarkFoo-8    1000000    1234.5 ns/op    64 B/op    2 allocs/op
BenchmarkBar-8    500       999999 ns/op    128 B/op    5 allocs/op
BenchmarkBaz-8    100       500.0 ns/op    0 B/op    0 allocs/op    200.5 MB/s
PASS
`)
	sum := &Summary{}
	parseOutput(data, sum)

	if len(sum.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(sum.Results))
	}

	foo := sum.Results[0]
	if foo.Name != "BenchmarkFoo" {
		t.Errorf("name = %q, want BenchmarkFoo", foo.Name)
	}
	if foo.NsPerOp != 1234.5 {
		t.Errorf("ns/op = %f, want 1234.5", foo.NsPerOp)
	}
	if foo.BPerOp != 64 {
		t.Errorf("B/op = %f, want 64", foo.BPerOp)
	}
	if foo.AllocsPerOp != 2 {
		t.Errorf("allocs/op = %f, want 2", foo.AllocsPerOp)
	}
	if foo.Iterations != 1000000 {
		t.Errorf("iterations = %d, want 1000000", foo.Iterations)
	}

	baz := sum.Results[2]
	if baz.MBPerSec != 200.5 {
		t.Errorf("MB/s = %f, want 200.5", baz.MBPerSec)
	}
}

func TestParseOutput_WithCPUSuffix(t *testing.T) {
	data := []byte(`BenchmarkProcessRow/small-16    2000    750.0 ns/op`)
	sum := &Summary{}
	parseOutput(data, sum)
	if len(sum.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(sum.Results))
	}
	if sum.Results[0].Name != "BenchmarkProcessRow/small" {
		t.Errorf("name = %q, want BenchmarkProcessRow/small", sum.Results[0].Name)
	}
}

func TestParseOutput_Failure(t *testing.T) {
	data := []byte(`FAIL    github.com/foo/bar [build failed]`)
	sum := &Summary{}
	parseOutput(data, sum)
	if len(sum.Failures) != 1 {
		t.Errorf("expected 1 failure, got %d", len(sum.Failures))
	}
}
