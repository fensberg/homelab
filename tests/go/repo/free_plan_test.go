package repo

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every Worker the estate declares is counted against the free plan.
//
// What runs at the vendor for nothing is held to what the vendor gives for
// nothing: each Worker says what it uses in a day, the uses are added up, and
// a plan that asks for more is refused before anything changes (the
// `free_plan_use` output, proved by the estate root's own tests). That sum is
// only the truth while everything is in it. A Worker declared and left out
// uses its share of the day's requests and writes all the same, and the first
// anybody knows is the vendor refusing the rest - silently, part way through a
// day, for everything on the account.
//
// So a Worker is found by what it is, wherever it is declared, and must be
// named in the map the sum is made from.
func TestEveryWorkerIsCountedAgainstTheFreePlan(t *testing.T) {
	declared := regexp.MustCompile(`(?m)^resource\s+"cloudflare_workers_script"\s+"([a-z0-9_]+)"`)
	_, counting := tofuDeclaring(t, "workers_use = {")
	block := counting[strings.Index(counting, "workers_use = {"):]
	block = block[:strings.Index(block, "}")]

	var workers []string
	for rel, body := range tofuSources(t) {
		for _, m := range declared.FindAllStringSubmatch(body, -1) {
			workers = append(workers, m[1]+" ("+rel+")")
			if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(m[1]) + `\s*=`).MatchString(block) {
				t.Errorf("%s declares the Worker %q, and it is not counted against the free plan.\n\n"+
					"Say what it uses in a day - timers, requests, reads and writes - and name it in workers_use. "+
					"Uncounted, it uses its share all the same, and what goes over is refused at the vendor "+
					"for everything on the account.", rel, m[1])
			}
		}
	}
	sort.Strings(workers)
	if len(workers) == 0 {
		t.Fatal("found no Worker declared anywhere, so nothing was checked: the reader has stopped matching, or the sum is of nothing")
	}
}
