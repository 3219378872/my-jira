package quality

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func testArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write([]byte(body)); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return buf.Bytes()
}

const sarifDefect = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"fixture-analyzer"}},"invocations":[{"executionSuccessful":true}],"results":[{"ruleId":"unsafe-query","level":"error","message":{"text":"SQL query contains untrusted input"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"server/query.go"},"region":{"startLine":27}}}]}]}]}`

func TestThreeDimensionsPinKnownDefectAndTestOutcomes(t *testing.T) {
	z := testArchive(t, map[string]string{"reports/code.sarif": sarifDefect, "reports/junit.xml": `<testsuites><testsuite><testcase name="pass"/><testcase name="skip"><skipped/></testcase><testcase classname="query" name="unsafe" file="server/query_test.go" line="42"><failure message="SQL injection reproduced"/></testcase></testsuite></testsuites>`, "coverage/lcov.info": "SF:server/query.go\nDA:1,1\nDA:2,0\nend_of_record\n"})
	files, e := unpackReports(z, 92, "immutable-archive-digest")
	if e != nil {
		t.Fatal(e)
	}
	docs := []documentSource{{ID: "story-a", Kind: "story", Name: "Search", Version: 7, Role: "Reader", Goal: "Search", Benefit: "Find items", Criteria: []string{"user must access secret", "user must not access secret"}, ContentHash: "document-seven", Linked: true}}
	dim, findings := analyze(repositoryEvidence{CommitSHA: strings.Repeat("a", 40), RunID: 15, RunAttempt: 2, RunConclusion: "failure", Reports: files}, docs)
	if dim.Code.Status != "failed" || dim.Documentation.Status != "failed" || dim.Tests.Status != "failed" {
		t.Fatalf("known defects not found: %#v", dim)
	}
	if dim.Tests.Summary["passed"] != 1 || dim.Tests.Summary["failed"] != 1 || dim.Tests.Summary["skipped"] != 1 || dim.Tests.Summary["coverage_percent"] != float64(50) {
		t.Fatalf("CI outcomes not preserved: %#v", dim.Tests)
	}
	code, contradiction := false, false
	for _, f := range findings {
		if f.Rule == "unsafe-query" {
			code = f.Path == "server/query.go" && f.Line == 27 && f.Evidence["artifact_id"] == int64(92) && f.Evidence["artifact_sha256"] == "immutable-archive-digest" && f.Actionable
		}
		if f.Rule == "acceptance.contradiction" {
			contradiction = f.Evidence["source_version"] == int64(7) && f.Evidence["content_sha256"] == "document-seven"
		}
	}
	if !code || !contradiction {
		t.Fatalf("finding provenance lost: %#v", findings)
	}
}

func TestMissingSkippedFailedAndUnknownAreNotPassing(t *testing.T) {
	for _, tc := range []struct{ name, xml, want string }{
		{"missing", "", "missing"},
		{"skipped", `<testsuite><testcase name="skip"><skipped/></testcase></testsuite>`, "skipped"},
		{"empty", `<testsuite tests="0"/>`, "unknown"},
		{"broken", `<testsuite><testcase>`, "unknown"},
		{"failed", `<testsuite><testcase name="a"><failure/></testcase><testcase name="b"><skipped/></testcase></testsuite>`, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := repositoryEvidence{RunConclusion: "success"}
			if tc.xml != "" {
				e.Reports = []reportFile{{Name: "junit.xml", Data: []byte(tc.xml)}}
			}
			dim, _ := analyze(e, nil)
			if dim.Tests.Status != tc.want {
				t.Fatalf("got %q want %q", dim.Tests.Status, tc.want)
			}
			if dim.Tests.Summary["coverage_status"] != "unknown" || dim.Documentation.Summary["requirement_mapping"] != "unknown" || dim.Code.Status != "unknown" {
				t.Fatalf("missing evidence was presented as verified: %#v", dim)
			}
		})
	}
}

func TestUnsafeArchivesAndXMLRejected(t *testing.T) {
	for _, name := range []string{"../secret.xml", "/absolute.xml", "report/../../secret.xml", `..\secret.xml`, `C:\secret.xml`} {
		if _, e := unpackReports(testArchive(t, map[string]string{name: "<testsuite/>"}), 1, "hash"); e == nil {
			t.Errorf("accepted %q", name)
		}
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "report.xml"}
	h.SetMode(os.ModeSymlink | 0777)
	f, e := w.CreateHeader(h)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.Write([]byte("/etc/passwd"))
	_ = w.Close()
	if _, e = unpackReports(b.Bytes(), 1, "hash"); e == nil {
		t.Fatal("accepted symlink artifact")
	}
	if _, e := unpackReports(testArchive(t, map[string]string{"large.xml": strings.Repeat("x", (8<<20)+1)}), 1, "hash"); e == nil {
		t.Fatal("accepted oversized expanded file")
	}
	_, _, e = readJUnit(reportFile{Name: "junit.xml", Data: []byte(`<!DOCTYPE testsuite [<!ENTITY test SYSTEM "file:///etc/passwd">]><testsuite><testcase name="a">&test;</testcase></testsuite>`)})
	if e == nil {
		t.Fatal("accepted XML entity directive")
	}
}

func TestDocumentMappingBoundaryAndRevision(t *testing.T) {
	if containsIdentifier("MAIN-100", "MAIN-10") {
		t.Fatal("substring matched another requirement")
	}
	if !containsIdentifier("Fixes MAIN-10.", "MAIN-10") {
		t.Fatal("missed exact work item")
	}
	e := repositoryEvidence{Repository: "org/repo", CommitSHA: strings.Repeat("b", 40), RunID: 12, RunAttempt: 2, Artifacts: []artifactEvidence{{ID: 11, Digest: "sha-one", Revision: "artifact-v1"}}}
	docs := []documentSource{{ID: "a", Version: 1, ContentHash: "v1"}}
	a := sourceRevision(e, docs)
	e.RunAttempt = 3
	if a == sourceRevision(e, docs) {
		t.Fatal("rerun mixed artifact provenance")
	}
	e.RunAttempt = 2
	docs[0].Version = 2
	if a == sourceRevision(e, docs) {
		t.Fatal("document versions lost")
	}
	docs[0].Version = 1
	e.Artifacts[0].Digest = "sha-two"
	if a == sourceRevision(e, docs) {
		t.Fatal("artifact revision lost")
	}
	dim, findings := analyze(repositoryEvidence{}, []documentSource{{ID: "d", Kind: "story", Version: 1, Name: "完整叙述", Role: "用户", Goal: "访问", Benefit: "查找", Criteria: []string{"用户必须访问资源", "用户不得访问资源"}}})
	if dim.Documentation.Status != "failed" || len(findings) != 1 {
		raw, _ := json.Marshal(dim)
		t.Fatalf("explicit Chinese contradiction not found: %s", raw)
	}
}

func TestCrossDocumentContradictionPinsBothVersions(t *testing.T) {
	docs := []documentSource{{ID: "prd", Kind: "page", Version: 11, Name: "PRD", Content: "<p>User must not access private reports.</p>", ContentHash: "prd-eleven"}, {ID: "story", Kind: "story", Version: 4, Name: "Reports", Role: "User", Goal: "Read", Benefit: "Review", Criteria: []string{"User must access private reports."}, ContentHash: "story-four"}}
	_, findings := analyze(repositoryEvidence{}, docs)
	found := false
	for _, f := range findings {
		if f.Rule == "source.contradiction" {
			found = f.Evidence["source_version"] == int64(4) && f.Evidence["other_source_version"] == int64(11)
		}
	}
	if !found {
		t.Fatalf("PRD contradiction lost source versions: %#v", findings)
	}
}
