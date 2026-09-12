package quality

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type reportFile struct {
	Name       string
	ArtifactID int64
	Digest     string
	Data       []byte
}

func containsIdentifier(text, id string) bool {
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-')
	}) {
		if strings.EqualFold(part, id) {
			return true
		}
	}
	return false
}

type Finding struct {
	ID          string         `json:"id"`
	Dimension   string         `json:"dimension"`
	Rule        string         `json:"rule"`
	Severity    string         `json:"severity"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Path        string         `json:"path,omitempty"`
	Line        int            `json:"line,omitempty"`
	Status      string         `json:"status"`
	Actionable  bool           `json:"actionable"`
	Evidence    map[string]any `json:"evidence"`
}
type Dimension struct {
	Status   string         `json:"status"`
	Findings int            `json:"findings"`
	Summary  map[string]any `json:"summary"`
}
type Dimensions struct {
	Code          Dimension `json:"code"`
	Documentation Dimension `json:"documentation"`
	Tests         Dimension `json:"tests"`
}
type documentSource struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Version     int64    `json:"version"`
	Name        string   `json:"name"`
	Identifier  string   `json:"identifier,omitempty"`
	Role        string   `json:"role,omitempty"`
	Goal        string   `json:"goal,omitempty"`
	Benefit     string   `json:"benefit,omitempty"`
	Criteria    []string `json:"criteria,omitempty"`
	Content     string   `json:"content,omitempty"`
	ContentHash string   `json:"content_sha256"`
	Linked      bool     `json:"linked_to_change"`
}

func unpackReports(raw []byte, artifactID int64, digest string) ([]reportFile, error) {
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return nil, errors.New("invalid report archive")
	}
	if len(z.File) > 200 {
		return nil, errors.New("report archive has too many entries")
	}
	files := []reportFile{}
	var total uint64
	for _, f := range z.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if strings.HasPrefix(name, "/") || strings.Contains(name, ":") || path.Clean(name) != strings.TrimSuffix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || f.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe report archive entry")
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > 8<<20 {
			return nil, errors.New("report file is too large")
		}
		total += f.UncompressedSize64
		if total > 32<<20 {
			return nil, errors.New("expanded report archive is too large")
		}
		lower := strings.ToLower(name)
		if !(strings.HasSuffix(lower, ".sarif") || strings.HasSuffix(lower, ".sarif.json") || strings.HasSuffix(lower, ".xml") || strings.HasSuffix(lower, ".lcov") || path.Base(lower) == "lcov.info" || path.Base(lower) == "coverage-summary.json") {
			continue
		}
		r, e := f.Open()
		if e != nil {
			return nil, errors.New("report archive could not be read")
		}
		data, e := limitedRead(r, 8<<20)
		r.Close()
		if e != nil {
			return nil, e
		}
		files = append(files, reportFile{Name: name, ArtifactID: artifactID, Digest: digest, Data: data})
	}
	return files, nil
}

func findingID(f Finding) string {
	raw, _ := json.Marshal([]any{f.Dimension, f.Rule, f.Title, f.Path, f.Line, f.Evidence})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:12])
}

func fileEvidence(f reportFile) map[string]any {
	return map[string]any{"artifact_id": f.ArtifactID, "artifact_sha256": f.Digest, "report_path": f.Name}
}

func analyze(e repositoryEvidence, docs []documentSource) (Dimensions, []Finding) {
	dim := Dimensions{Code: Dimension{Status: "unknown", Summary: map[string]any{"static_analysis": "missing", "source_files": len(e.Files), "source_truncated": e.Truncated}}, Documentation: Dimension{Status: "unknown", Summary: map[string]any{"sources": len(docs), "requirement_mapping": "unknown", "semantic_implementation_alignment": "unknown"}}, Tests: Dimension{Status: "missing", Summary: map[string]any{"passed": 0, "failed": 0, "skipped": 0, "total": 0, "coverage_status": "unknown", "run_status": e.RunStatus, "run_conclusion": e.RunConclusion}}}
	findings := []Finding{}
	sarifFound, sarifInvalid, testFound, testInvalid, coverageFound := false, false, false, false, false
	passed, failed, skipped := 0, 0, 0
	for _, file := range e.Reports {
		lower := strings.ToLower(file.Name)
		if strings.HasSuffix(lower, ".sarif") || strings.HasSuffix(lower, ".sarif.json") {
			fs, err := readSARIF(file)
			if err != nil {
				sarifInvalid = true
				continue
			}
			sarifFound = true
			findings = append(findings, fs...)
			continue
		}
		if strings.HasSuffix(lower, ".xml") {
			root, err := xmlRoot(file.Data)
			if err != nil {
				testInvalid = true
				continue
			}
			switch root {
			case "testsuites", "testsuite":
				counts, fs, err := readJUnit(file)
				if err != nil {
					testInvalid = true
					continue
				}
				testFound = true
				passed += counts[0]
				failed += counts[1]
				skipped += counts[2]
				findings = append(findings, fs...)
			case "coverage":
				rate, err := readCobertura(file.Data)
				if err == nil {
					coverageFound = true
					dim.Tests.Summary["coverage_percent"] = rate
					dim.Tests.Summary["coverage_evidence"] = fileEvidence(file)
				}
			}
		}
		if strings.HasSuffix(lower, ".lcov") || path.Base(lower) == "lcov.info" {
			if rate, err := readLCOV(file.Data); err == nil {
				coverageFound = true
				dim.Tests.Summary["coverage_percent"] = rate
				dim.Tests.Summary["coverage_evidence"] = fileEvidence(file)
			}
		}
		if path.Base(lower) == "coverage-summary.json" {
			if rate, err := readCoverageSummary(file.Data); err == nil {
				coverageFound = true
				dim.Tests.Summary["coverage_percent"] = rate
				dim.Tests.Summary["coverage_evidence"] = fileEvidence(file)
			}
		}
	}
	if sarifFound {
		dim.Code.Status = "passed"
		dim.Code.Summary["static_analysis"] = "available"
	}
	if sarifInvalid {
		dim.Code.Status = "unknown"
		dim.Code.Summary["static_analysis"] = "invalid"
	}
	if coverageFound {
		dim.Tests.Summary["coverage_status"] = "available"
	}
	dim.Tests.Summary["passed"], dim.Tests.Summary["failed"], dim.Tests.Summary["skipped"], dim.Tests.Summary["total"] = passed, failed, skipped, passed+failed+skipped
	if testFound {
		dim.Tests.Status = "passed"
		if passed+failed == 0 {
			dim.Tests.Status = "skipped"
		}
		if passed+failed+skipped == 0 {
			dim.Tests.Status = "unknown"
		}
	}
	if testInvalid {
		dim.Tests.Status = "unknown"
		dim.Tests.Summary["invalid_reports"] = true
	}
	if failed > 0 {
		dim.Tests.Status = "failed"
	}
	if e.RunID > 0 && e.RunStatus != "completed" && failed == 0 {
		dim.Tests.Status = "unknown"
	}
	if (e.RunConclusion == "failure" || e.RunConclusion == "timed_out") && failed == 0 {
		// A build/deploy workflow may fail before any tests execute. Its failure
		// is actionable CI evidence, while absent test results remain unknown.
		dim.Tests.Status = "unknown"
		findings = append(findings, Finding{Dimension: "tests", Rule: "actions.failed", Severity: "high", Title: "GitHub Actions run failed", Description: "The pinned Actions run did not succeed. No failing test case is available; inspect the workflow stages and existing logs before attributing this failure to tests.", Status: "unknown", Actionable: true, Evidence: map[string]any{"run_id": e.RunID, "run_attempt": e.RunAttempt, "commit_sha": e.CommitSHA, "conclusion": e.RunConclusion}})
	}
	if !testFound && !testInvalid && (e.RunConclusion == "skipped" || e.RunConclusion == "cancelled") {
		dim.Tests.Status = "skipped"
	}
	if !testFound {
		dim.Tests.Summary["test_report_status"] = "missing"
	} else {
		dim.Tests.Summary["test_report_status"] = "available"
	}
	if len(e.Artifacts) > 0 {
		for _, a := range e.Artifacts {
			if a.Status != "available" && a.Status != "missing" {
				dim.Tests.Summary["artifact_gaps"] = true
				if dim.Tests.Status == "passed" || dim.Tests.Status == "missing" {
					dim.Tests.Status = "unknown"
				}
				if dim.Code.Status == "passed" {
					dim.Code.Status = "unknown"
				}
			}
		}
	}
	docFindings, mapped := analyzeDocuments(docs)
	findings = append(findings, docFindings...)
	if len(docs) > 0 {
		dim.Documentation.Summary["structural_check"] = "completed"
	}
	if mapped > 0 {
		dim.Documentation.Summary["requirement_mapping"] = "available"
		dim.Documentation.Summary["mapped_sources"] = mapped
		dim.Documentation.Status = "passed"
	}
	for i := range findings {
		f := &findings[i]
		f.ID = findingID(*f)
		switch f.Dimension {
		case "code":
			dim.Code.Findings++
			if f.Status == "failed" {
				dim.Code.Status = "failed"
			}
		case "documentation":
			dim.Documentation.Findings++
			if f.Status == "failed" {
				dim.Documentation.Status = "failed"
			}
		case "tests":
			dim.Tests.Findings++
		}
	}
	if len(findings) > 1000 {
		findings = findings[:1000]
		dim.Code.Summary["findings_truncated"] = true
		dim.Documentation.Summary["findings_truncated"] = true
		dim.Tests.Summary["findings_truncated"] = true
	}
	return dim, findings
}

func readSARIF(file reportFile) ([]Finding, error) {
	var report struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name string `json:"name"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID  string `json:"ruleId"`
				Level   string `json:"level"`
				Message struct {
					Text     string `json:"text"`
					Markdown string `json:"markdown"`
				} `json:"message"`
				Suppressions []struct {
					Status string `json:"status"`
				} `json:"suppressions"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
			Invocations []struct {
				ExecutionSuccessful bool `json:"executionSuccessful"`
			} `json:"invocations"`
		} `json:"runs"`
	}
	if json.Unmarshal(file.Data, &report) != nil || report.Version != "2.1.0" || len(report.Runs) == 0 {
		return nil, errors.New("invalid SARIF report")
	}
	result := []Finding{}
	for _, run := range report.Runs {
		for _, inv := range run.Invocations {
			if !inv.ExecutionSuccessful {
				return nil, errors.New("SARIF analyzer did not complete")
			}
		}
		if len(run.Results) > 10000 {
			return nil, errors.New("SARIF result limit exceeded")
		}
		for _, r := range run.Results {
			suppressed := false
			for _, suppression := range r.Suppressions {
				if suppression.Status == "accepted" {
					suppressed = true
				}
			}
			if suppressed || r.Level == "none" {
				continue
			}
			message := r.Message.Text
			if message == "" {
				message = r.Message.Markdown
			}
			if len(message) > 4000 {
				message = message[:4000]
			}
			f := Finding{Dimension: "code", Rule: r.RuleID, Severity: "medium", Title: message, Description: message, Status: "failed", Evidence: fileEvidence(file)}
			if r.Level == "error" {
				f.Severity = "high"
			}
			if r.Level == "note" {
				f.Severity = "low"
				f.Status = "unknown"
			}
			f.Evidence["analyzer"] = run.Tool.Driver.Name
			if len(r.Locations) > 0 {
				loc := r.Locations[0].PhysicalLocation
				f.Path = loc.ArtifactLocation.URI
				f.Line = loc.Region.StartLine
			}
			f.Actionable = f.Path != "" && f.Line > 0 && f.Status == "failed"
			result = append(result, f)
		}
	}
	return result, nil
}

func xmlRoot(raw []byte) (string, error) {
	d := xml.NewDecoder(bytes.NewReader(raw))
	for {
		t, e := d.Token()
		if e != nil {
			return "", e
		}
		if _, ok := t.(xml.Directive); ok {
			return "", errors.New("XML directives are not allowed")
		}
		if s, ok := t.(xml.StartElement); ok {
			return s.Name.Local, nil
		}
	}
}

func readJUnit(file reportFile) ([3]int, []Finding, error) {
	counts := [3]int{}
	findings := []Finding{}
	d := xml.NewDecoder(bytes.NewReader(file.Data))
	depth := 0
	cases := 0
	for {
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return counts, nil, e
		}
		switch tok.(type) {
		case xml.Directive:
			return counts, nil, errors.New("XML directives are not allowed")
		case xml.EndElement:
			depth--
			continue
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		depth++
		if depth > 100 {
			return counts, nil, errors.New("JUnit nesting limit exceeded")
		}
		if start.Name.Local != "testcase" {
			continue
		}
		cases++
		if cases > 50000 {
			return counts, nil, errors.New("JUnit case limit exceeded")
		}
		var item struct {
			Name     string `xml:"name,attr"`
			Class    string `xml:"classname,attr"`
			File     string `xml:"file,attr"`
			Line     int    `xml:"line,attr"`
			Failures []struct {
				Message string `xml:"message,attr"`
				Text    string `xml:",chardata"`
			} `xml:"failure"`
			Errors []struct {
				Message string `xml:"message,attr"`
				Text    string `xml:",chardata"`
			} `xml:"error"`
			Skipped *struct{} `xml:"skipped"`
		}
		if e = d.DecodeElement(&item, &start); e != nil {
			return counts, nil, e
		}
		depth--
		if len(item.Failures) > 0 || len(item.Errors) > 0 {
			counts[1]++
			message := "Test failed"
			if len(item.Failures) > 0 {
				message = item.Failures[0].Message
			}
			if len(item.Errors) > 0 {
				message = item.Errors[0].Message
			}
			if len(message) > 4000 {
				message = message[:4000]
			}
			ev := fileEvidence(file)
			ev["test_name"], ev["test_class"] = item.Name, item.Class
			findings = append(findings, Finding{Dimension: "tests", Rule: "junit.failure", Severity: "high", Title: "Failing test: " + item.Name, Description: message, Path: item.File, Line: item.Line, Status: "failed", Actionable: true, Evidence: ev})
		} else if item.Skipped != nil {
			counts[2]++
		} else {
			counts[0]++
		}
	}
	return counts, findings, nil
}

func readCobertura(raw []byte) (float64, error) {
	var report struct {
		Rate    string `xml:"line-rate,attr"`
		Valid   int    `xml:"lines-valid,attr"`
		Covered int    `xml:"lines-covered,attr"`
	}
	if e := xml.Unmarshal(raw, &report); e != nil {
		return 0, e
	}
	if report.Valid > 0 && report.Covered >= 0 && report.Covered <= report.Valid {
		return float64(report.Covered) * 100 / float64(report.Valid), nil
	}
	rate, e := strconv.ParseFloat(report.Rate, 64)
	if e != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
		return 0, errors.New("invalid coverage")
	}
	return rate * 100, nil
}
func readLCOV(raw []byte) (float64, error) {
	total, covered := 0, 0
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "DA:") {
			parts := strings.Split(strings.TrimPrefix(line, "DA:"), ",")
			if len(parts) < 2 {
				return 0, errors.New("invalid LCOV")
			}
			hits, e := strconv.Atoi(parts[1])
			if e != nil || hits < 0 {
				return 0, errors.New("invalid LCOV")
			}
			total++
			if hits > 0 {
				covered++
			}
		}
	}
	if total == 0 {
		return 0, errors.New("coverage has no line evidence")
	}
	return float64(covered) * 100 / float64(total), nil
}
func readCoverageSummary(raw []byte) (float64, error) {
	var value struct {
		Total struct {
			Lines struct {
				Total   int `json:"total"`
				Covered int `json:"covered"`
			} `json:"lines"`
		} `json:"total"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Total.Lines.Total <= 0 || value.Total.Lines.Covered < 0 || value.Total.Lines.Covered > value.Total.Lines.Total {
		return 0, errors.New("invalid coverage summary")
	}
	return float64(value.Total.Lines.Covered) * 100 / float64(value.Total.Lines.Total), nil
}

func analyzeDocuments(docs []documentSource) ([]Finding, int) {
	findings := []Finding{}
	mapped := 0
	for _, doc := range docs {
		if doc.Linked {
			mapped++
		}
		if doc.Kind != "story" {
			continue
		}
		evidence := map[string]any{"source_id": doc.ID, "source_kind": doc.Kind, "source_version": doc.Version, "content_sha256": doc.ContentHash}
		missing := []string{}
		if strings.TrimSpace(doc.Role) == "" {
			missing = append(missing, "role")
		}
		if strings.TrimSpace(doc.Goal) == "" {
			missing = append(missing, "goal")
		}
		if strings.TrimSpace(doc.Benefit) == "" {
			missing = append(missing, "benefit")
		}
		if len(doc.Criteria) == 0 {
			missing = append(missing, "acceptance_criteria")
		}
		if len(missing) > 0 {
			findings = append(findings, Finding{Dimension: "documentation", Rule: "story.incomplete", Severity: "medium", Title: "Incomplete story: " + doc.Name, Description: "Missing required story fields: " + strings.Join(missing, ", "), Status: "failed", Actionable: true, Evidence: evidence})
		}
		seen := map[string]struct {
			negative bool
			index    int
			text     string
		}{}
		for i, criterion := range doc.Criteria {
			key, negative := criterionPolarity(criterion)
			if key == "" {
				continue
			}
			if previous, ok := seen[key]; ok && previous.negative != negative {
				ev := map[string]any{}
				for k, v := range evidence {
					ev[k] = v
				}
				ev["criterion_indexes"] = []int{previous.index, i}
				ev["criteria"] = []string{previous.text, criterion}
				findings = append(findings, Finding{Dimension: "documentation", Rule: "acceptance.contradiction", Severity: "high", Title: "Contradictory acceptance criteria: " + doc.Name, Description: "Two acceptance criteria require and prohibit the same stated behavior. Resolve the contradictory source requirements.", Status: "failed", Actionable: true, Evidence: ev})
			} else {
				seen[key] = struct {
					negative bool
					index    int
					text     string
				}{negative, i, criterion}
			}
		}
	}
	findings = append(findings, crossDocumentContradictions(docs)...)
	return findings, mapped
}

var htmlTags = regexp.MustCompile(`<[^>]+>`)

func crossDocumentContradictions(docs []documentSource) []Finding {
	type claim struct {
		doc      documentSource
		text     string
		index    int
		negative bool
	}
	seen := map[string][]claim{}
	result := []Finding{}
	dedup := map[string]bool{}
	for _, doc := range docs {
		criteria := doc.Criteria
		if doc.Kind == "page" {
			text := strings.NewReplacer("</p>", "\n", "</li>", "\n", "<br>", "\n", "<br/>", "\n").Replace(doc.Content)
			text = html.UnescapeString(htmlTags.ReplaceAllString(text, ""))
			criteria = strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '。' })
		}
		for i, text := range criteria {
			normative := false
			lower := strings.ToLower(text)
			for _, marker := range []string{"must ", "shall ", "should ", "必须", "不得", "不能", "不允许", "应当"} {
				if strings.Contains(lower, marker) {
					normative = true
				}
			}
			if !normative {
				continue
			}
			key, negative := criterionPolarity(text)
			if key == "" {
				continue
			}
			for _, other := range seen[key] {
				if other.doc.ID == doc.ID || other.negative == negative {
					continue
				}
				pair := other.doc.ID + ":" + doc.ID + ":" + key
				if dedup[pair] {
					continue
				}
				dedup[pair] = true
				result = append(result, Finding{Dimension: "documentation", Rule: "source.contradiction", Severity: "high", Title: "Conflicting source requirements: " + doc.Name, Description: "The pinned PRD/story sources require and prohibit the same explicit behavior. Resolve the source contradiction before implementation.", Status: "failed", Actionable: true, Evidence: map[string]any{"source_id": doc.ID, "source_kind": doc.Kind, "source_version": doc.Version, "content_sha256": doc.ContentHash, "criterion_index": i, "criterion": text, "other_source_id": other.doc.ID, "other_source_kind": other.doc.Kind, "other_source_version": other.doc.Version, "other_content_sha256": other.doc.ContentHash, "other_criterion_index": other.index, "other_criterion": other.text}})
				if len(result) >= 500 {
					return result
				}
			}
			if len(seen[key]) < 64 {
				seen[key] = append(seen[key], claim{doc, text, i, negative})
			}
		}
	}
	return result
}

// This deliberately checks explicit positive/negative duplicates only. Broader
// semantic contradiction and implementation conformance remain unknown.
func criterionPolarity(text string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(text))
	negative := false
	for _, marker := range []string{"must not ", "shall not ", "should not ", "不得", "不能", "不允许"} {
		if strings.Contains(s, marker) {
			negative = true
			s = strings.ReplaceAll(s, marker, "")
		}
	}
	for _, marker := range []string{"must ", "shall ", "should ", "必须", "应当", "允许"} {
		s = strings.ReplaceAll(s, marker, "")
	}
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, " .。!！;")
	return s, negative
}

func sourceRevision(e repositoryEvidence, docs []documentSource) string {
	versions := []string{}
	for _, d := range docs {
		versions = append(versions, fmt.Sprintf("%s:%s:%d:%s", d.Kind, d.ID, d.Version, d.ContentHash))
	}
	sort.Strings(versions)
	artifacts := append([]artifactEvidence{}, e.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].ID == artifacts[j].ID {
			return artifacts[i].Name < artifacts[j].Name
		}
		return artifacts[i].ID < artifacts[j].ID
	})
	raw, _ := json.Marshal([]any{e.Repository, e.CommitSHA, e.PullRequest, e.PRBaseSHA, e.RunID, e.RunAttempt, e.RunStatus, e.RunConclusion, artifacts, versions, "quality-v1"})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
