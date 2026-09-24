package harness

import (
	"strings"
	"testing"
)

// A trimmed answer from /api/v1/targets, shaped as Prometheus returns it.
// Addresses are RFC 5737 documentation ranges.
const targetsFixture = `{
  "status": "success",
  "data": {
    "activeTargets": [
      {"labels": {"job": "kube-etcd"}, "scrapeUrl": "http://192.0.2.10:2381/metrics", "health": "down",
       "lastError": "Get \"http://192.0.2.10:2381/metrics\": dial tcp 192.0.2.10:2381: connect: connection refused"},
      {"labels": {"job": "kube-etcd"}, "scrapeUrl": "http://192.0.2.11:2381/metrics", "health": "up", "lastError": ""},
      {"labels": {"job": "kube-etcd"}, "scrapeUrl": "http://192.0.2.12:2381/metrics", "health": "unknown", "lastError": ""},
      {"labels": {"job": "kube-scheduler"}, "scrapeUrl": "https://192.0.2.10:10259/metrics", "health": "down",
       "lastError": "some other job's failure"}
    ]
  }
}`

// The report carries Prometheus's recorded error, not a guess.
//
// This is the whole point of the function: the earlier test message offered
// two possible causes when the real one was sitting in this field.
func TestScrapeFailuresReportsTheRecordedError(t *testing.T) {
	failures, total, err := ScrapeFailures([]byte(targetsFixture), "kube-etcd")
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("counted %d kube-etcd targets, want 3", total)
	}
	if len(failures) != 2 {
		t.Fatalf("reported %d failures, want 2 (one down, one never scraped): %v", len(failures), failures)
	}

	joined := strings.Join(failures, "\n")
	if !strings.Contains(joined, "connection refused") {
		t.Errorf("the report does not carry Prometheus's recorded error:\n%s", joined)
	}
	if !strings.Contains(joined, "has not been scraped") {
		t.Errorf("a target with no recorded error is not explained, so it reads as healthy:\n%s", joined)
	}
}

// Only the job asked about is reported, and a healthy target is not.
func TestScrapeFailuresKeepsToItsJob(t *testing.T) {
	failures, _, err := ScrapeFailures([]byte(targetsFixture), "kube-etcd")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range failures {
		if strings.Contains(f, "some other job") {
			t.Errorf("reported another job's target: %s", f)
		}
		if strings.Contains(f, "192.0.2.11") {
			t.Errorf("reported a healthy target as a failure: %s", f)
		}
	}
}

// "No targets" and "failing targets" must stay distinguishable - they are
// different faults. A job with no targets reports zero, not an empty success.
func TestScrapeFailuresDistinguishesNoTargets(t *testing.T) {
	failures, total, err := ScrapeFailures([]byte(targetsFixture), "kube-apiserver")
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(failures) != 0 {
		t.Errorf("a job Prometheus has never heard of returned total=%d failures=%v", total, failures)
	}
}

// An answer Prometheus refused is an error, not an empty report.
func TestScrapeFailuresRefusesAFailedAnswer(t *testing.T) {
	if _, _, err := ScrapeFailures([]byte(`{"status":"error","data":{}}`), "kube-etcd"); err == nil {
		t.Error("a failed answer from Prometheus was read as an empty target list")
	}
	if _, _, err := ScrapeFailures([]byte(`not json`), "kube-etcd"); err == nil {
		t.Error("an undecodable answer was read as an empty target list")
	}
}
