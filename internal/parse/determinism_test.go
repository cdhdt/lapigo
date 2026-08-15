package parse

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// determinismWorkerEnv, when set, tells
// TestParse_SuggestionIsDeterministicAcrossProcesses to act as the worker
// half of its own subprocess harness instead of the driving half.
const determinismWorkerEnv = "LAPIGO_DETERMINISM_WORKER"

// determinismPayloadStart and determinismPayloadEnd bracket the line the
// worker half prints, so the driver can pull its one line of interest out
// of `go test -v`'s surrounding noise (PASS/RUN lines, timing) without
// caring about the exact shape of that noise.
const (
	determinismPayloadStart = "===DETERMINISM-PAYLOAD-START==="
	determinismPayloadEnd   = "===DETERMINISM-PAYLOAD-END==="
)

// determinismAmbiguousFixture triggers the unknown-endpoint suggestion path
// with a value equidistant (Levenshtein 2) from two vocabulary entries,
// "delete" and "get" -- see the test's own doc comment for the arithmetic.
const determinismAmbiguousFixture = "entities:\n" +
	"  article:\n" +
	"    fields:\n" +
	"      id: { type: uuid, pk: true }\n" +
	"    endpoints: [dete]\n"

// TestParse_SuggestionIsDeterministicAcrossProcesses is the regression test
// for defect 1: endpointKeywordNames and typeKeywordSuggestions were built
// by ranging a map (`for k := range <map>`), and suggest.go broke ties with
// a strict "<", so which of two equidistant vocabulary entries won a
// suggestion depended on Go's per-process randomised map iteration order.
//
// An in-process determinism test cannot catch this defect: the package-level
// vocabulary slices are built exactly once, at package init, by ranging the
// map a single time -- every call within one test process therefore sees
// the same (arbitrary, but fixed for that process) order, and the bug is
// invisible no matter how many times Parse is called from the same `go
// test` invocation.
//
// This test instead re-executes the compiled test binary itself as a fresh
// OS subprocess, several times, each with its own independent map seed. It
// runs Parse on a fixture containing `endpoints: [dete]` -- "dete" is
// Levenshtein distance 2 from both "delete" and "get", a genuine tie -- and
// asserts the rendered diagnostic (which embeds the suggested name) is
// byte-for-byte identical across every run. Before endpointKeywordNames and
// typeKeywordSuggestions were sorted and suggest's tie-break made
// deterministic (k < best on equal distance), this test failed intermittently:
// the review's own measurement saw 11 of 25 processes report one winner and
// 14 the other.
func TestParse_SuggestionIsDeterministicAcrossProcesses(t *testing.T) {
	if os.Getenv(determinismWorkerEnv) != "" {
		runDeterminismWorker(t)
		return
	}

	const runs = 25
	var first string
	for i := 0; i < runs; i++ {
		got := runDeterminismSubprocess(t, i)
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d's rendered diagnostic differs from run 0's -- nondeterministic suggestion across processes:\nrun 0: %s\nrun %d: %s",
				i, first, i, got)
		}
	}
}

// runDeterminismSubprocess launches the test binary as a fresh subprocess
// in worker mode, restricted to this same test (so the subprocess does not
// recursively spawn further subprocesses), and returns the one payload line
// it printed.
func runDeterminismSubprocess(t *testing.T, run int) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestParse_SuggestionIsDeterministicAcrossProcesses$")
	cmd.Env = append(os.Environ(), determinismWorkerEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess run %d failed: %v\noutput:\n%s", run, err, out)
	}
	payload, ok := extractDeterminismPayload(string(out))
	if !ok {
		t.Fatalf("subprocess run %d printed no payload between %s and %s:\n%s",
			run, determinismPayloadStart, determinismPayloadEnd, out)
	}
	return payload
}

// runDeterminismWorker runs when this test executes as the subprocess
// half: it parses determinismAmbiguousFixture and prints the rendered
// diagnostic, bracketed by the payload markers, to stdout.
func runDeterminismWorker(t *testing.T) {
	t.Helper()
	f := source.File{Name: "lapigo.yaml", Src: []byte(determinismAmbiguousFixture)}
	_, diags := Parse(f)
	if len(diags) != 1 {
		t.Fatalf("worker: len(diags) = %d, want 1: %+v", len(diags), diags)
	}
	fmt.Println(determinismPayloadStart)
	fmt.Println(diags.Render(f))
	fmt.Println(determinismPayloadEnd)
}

// extractDeterminismPayload pulls the single line bracketed by
// determinismPayloadStart/End out of s.
func extractDeterminismPayload(s string) (string, bool) {
	start := strings.Index(s, determinismPayloadStart)
	if start < 0 {
		return "", false
	}
	end := strings.Index(s, determinismPayloadEnd)
	if end < 0 || end < start {
		return "", false
	}
	body := s[start+len(determinismPayloadStart) : end]
	return strings.TrimSpace(body), true
}
