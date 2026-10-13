package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"homelab/details/hypervisorapi"
)

var noon = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

// What is done about a machine is decided by four things and nothing else:
// whether it has run anything since it was put back, whether it is running
// anything now, how long it has been in use, and whether it is already
// held back.
func TestWhatIsDoneAboutAMachine(t *testing.T) {
	const most = 30 * time.Minute
	for name, tc := range map[string]struct {
		m    machine
		want step
	}{
		"clean and quiet is left alone":                {machine{}, wait},
		"clean and given a job is marked in use":       {machine{Jobs: 2}, markInUse},
		"in use and busy, inside its time, is left":    {machine{Since: noon.Add(-29 * time.Minute), Jobs: 1}, wait},
		"in use and quiet is put back":                 {machine{Since: noon.Add(-time.Second)}, putBack},
		"in use for its whole time is held back":       {machine{Since: noon.Add(-most), Jobs: 1}, holdBack},
		"held back and still busy is waited for":       {machine{Since: noon.Add(-2 * most), Jobs: 1, HeldBack: true}, wait},
		"held back and quiet is put back":              {machine{Since: noon.Add(-2 * most), HeldBack: true}, putBack},
		"held back by somebody, never used, is left":   {machine{HeldBack: true}, wait},
		"quiet past its time is put back, not held":    {machine{Since: noon.Add(-2 * most)}, putBack},
		"clean with a job while held back is in use":   {machine{Jobs: 1, HeldBack: true}, markInUse},
		"in use, busy, a second short of its time":     {machine{Since: noon.Add(-most + time.Second), Jobs: 3}, wait},
		"in use and quiet is put back however briefly": {machine{Since: noon}, putBack},
	} {
		if got := next(tc.m, noon, most); got != tc.want {
			t.Errorf("%s: got step %d, want %d", name, got, tc.want)
		}
	}
}

// estate is a cluster and a hypervisor that remember what was done to them.
// The machine comes back, after being started, as a machine that has
// started since - unless it is told not to.
type estate struct {
	machines   []machine
	did        []string
	fail       map[string]error
	arrives    bool // a job is placed as the machine is held back
	staysAway  bool
	guestNames []string
}

func (e *estate) find(name string) *machine {
	for i := range e.machines {
		if e.machines[i].Name == name {
			return &e.machines[i]
		}
	}
	return nil
}

func (e *estate) Machines() ([]machine, error) {
	return append([]machine(nil), e.machines...), e.fail["machines"]
}

func (e *estate) Mark(name string, since *time.Time, heldBack bool) error {
	m := e.find(name)
	at := "clean"
	m.Since = time.Time{}
	if since != nil {
		m.Since, at = *since, "in use"
	}
	if heldBack && !m.HeldBack && e.arrives {
		m.Jobs++
	}
	m.HeldBack = heldBack
	e.did = append(e.did, fmt.Sprintf("mark %s %s held=%v", name, at, heldBack))
	return e.fail["mark"]
}

func (e *estate) Forget(name string) error {
	e.did = append(e.did, "forget "+name)
	return e.fail["forget"]
}

func (e *estate) List() ([]hypervisorapi.Guest, error) {
	var out []hypervisorapi.Guest
	for i, name := range e.guestNames {
		out = append(out, hypervisorapi.Guest{ID: 7300 + i, Name: name, Node: "host-a"})
	}
	return out, e.fail["list"]
}

func (e *estate) act(what string, g hypervisorapi.Guest) error {
	e.did = append(e.did, fmt.Sprintf("%s %d", what, g.ID))
	return e.fail[what]
}
func (e *estate) Stop(g hypervisorapi.Guest) error    { return e.act("stop", g) }
func (e *estate) PutBack(g hypervisorapi.Guest) error { return e.act("put back", g) }
func (e *estate) Delete(g hypervisorapi.Guest) error  { return e.act("delete", g) }
func (e *estate) Start(g hypervisorapi.Guest) error {
	if !e.staysAway {
		e.find(g.Name).Boot += "'"
	}
	return e.act("start", g)
}

func sweeping(e *estate) sweep {
	return sweep{cluster: e, hypervisor: e, most: 30 * time.Minute, now: func() time.Time { return noon },
		back: 0, pause: 0, say: func(string, ...any) {}}
}

func did(t *testing.T, e *estate, want ...string) {
	t.Helper()
	if strings.Join(e.did, "\n") != strings.Join(want, "\n") {
		t.Errorf("did:\n  %s\nwant:\n  %s", strings.Join(e.did, "\n  "), strings.Join(want, "\n  "))
	}
}

// A machine that ran something and is now quiet is held back, its power is
// cut, its disk put back and it is started; and only once it has come back
// as a machine started since is it given work again, as clean. A machine
// beside it that is busy is not touched.
func TestAQuietUsedMachineIsPutBackAndOnlyThenGivenWork(t *testing.T) {
	e := &estate{guestNames: []string{"plot-dmz-100", "plot-dmz-101"}, machines: []machine{
		{Name: "plot-dmz-100", Since: noon.Add(-time.Minute), Boot: "a", Ready: true},
		{Name: "plot-dmz-101", Since: noon.Add(-time.Minute), Boot: "b", Ready: true, Jobs: 1},
	}}
	if err := sweeping(e).once(); err != nil {
		t.Fatal(err)
	}
	did(t, e, "mark plot-dmz-100 in use held=true", "stop 7300", "put back 7300", "start 7300", "mark plot-dmz-100 clean held=false")
}

// The two marks before any put back: a machine given its first job is said
// to be in use, and one in use for its whole time is held back from new
// work with its job left running.
func TestAMachineIsMarkedInUseAndHeldBackWhenItsTimeIsUp(t *testing.T) {
	e := &estate{guestNames: []string{"plot-dmz-100", "plot-dmz-101"}, machines: []machine{
		{Name: "plot-dmz-100", Jobs: 1, Ready: true},
		{Name: "plot-dmz-101", Since: noon.Add(-31 * time.Minute), Jobs: 1, Ready: true},
	}}
	if err := sweeping(e).once(); err != nil {
		t.Fatal(err)
	}
	did(t, e, "mark plot-dmz-100 in use held=false", "mark plot-dmz-101 in use held=true")
	if !e.machines[0].Since.Equal(noon) {
		t.Errorf("the machine is in use since %v, not since it was seen with a job", e.machines[0].Since)
	}
}

// A job placed in the moment between the look and the holding back is left
// to finish: the machine keeps its power, stays held back and in use, and
// is put back by a later look.
func TestAJobThatArrivesAsAMachineIsHeldBackIsNotCutOff(t *testing.T) {
	e := &estate{arrives: true, guestNames: []string{"plot-dmz-100"}, machines: []machine{
		{Name: "plot-dmz-100", Since: noon.Add(-time.Minute), Boot: "a", Ready: true},
	}}
	if err := sweeping(e).once(); err != nil {
		t.Fatal(err)
	}
	did(t, e, "mark plot-dmz-100 in use held=true")
}

// A machine that cannot be put back is deleted, and its node with it, by
// whichever step failed: the hypervisor refusing the rollback, or the
// machine not coming back as one that started since. It is not tried again
// and it is never given work.
func TestAMachineThatCannotBePutBackIsDeleted(t *testing.T) {
	for name, e := range map[string]*estate{
		"the rollback fails":    {fail: map[string]error{"put back": errors.New("dataset is busy")}},
		"it does not come back": {staysAway: true},
	} {
		e.guestNames = []string{"plot-dmz-100"}
		e.machines = []machine{{Name: "plot-dmz-100", Since: noon.Add(-time.Minute), Boot: "a", Ready: true}}
		if err := sweeping(e).once(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		all := strings.Join(e.did, "\n")
		if !strings.HasSuffix(all, "stop 7300\ndelete 7300\nforget plot-dmz-100") {
			t.Errorf("%s: the machine was not deleted and forgotten:\n%s", name, all)
		}
		if strings.Contains(all, "clean") || strings.Count(all, "put back") > 1 {
			t.Errorf("%s: the machine was given work again, or put back twice:\n%s", name, all)
		}
	}

	// A machine the token cannot see is not this program's to delete, and
	// one that could be neither put back nor deleted is said, held back.
	unseen := &estate{machines: []machine{{Name: "plot-dmz-100", Since: noon.Add(-time.Minute)}}}
	if err := sweeping(unseen).once(); err == nil || strings.Contains(strings.Join(unseen.did, " "), "delete") {
		t.Errorf("a machine the hypervisor does not show was acted on, or passed over in silence: %v %v", err, unseen.did)
	}
	stuck := &estate{guestNames: []string{"plot-dmz-100"}, fail: map[string]error{"put back": errors.New("no"), "delete": errors.New("nor this")},
		machines: []machine{{Name: "plot-dmz-100", Since: noon.Add(-time.Minute)}}}
	if err := sweeping(stuck).once(); err == nil || strings.Contains(strings.Join(stuck.did, " "), "clean") || !stuck.machines[0].HeldBack {
		t.Errorf("a machine that could be neither put back nor deleted was let go: %v %v", err, stuck.did)
	}
}

// The cluster is read and marked as it is: an untrusted machine is a node
// with the zone's label, its jobs are the runners' pods on it that have not
// finished, and what is known about it is written on the node itself.
func TestTheClusterIsReadAndMarkedByItsNodes(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pod-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		asked = append(asked, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(body)))
		switch {
		case r.URL.Path == "/api/v1/nodes":
			fmt.Fprintf(w, `{"items":[{"metadata":{"name":"plot-dmz-100","annotations":{%q:"2026-03-04T11:40:00Z"}},
				"spec":{"unschedulable":true},
				"status":{"nodeInfo":{"bootID":"b-1"},"conditions":[{"type":"MemoryPressure","status":"False"},{"type":"Ready","status":"True"}]}}]}`, inUseSince)
		case strings.HasSuffix(r.URL.Path, "/pods"):
			fmt.Fprint(w, `{"items":[{"status":{"phase":"Running"}},{"status":{"phase":"Pending"}},{"status":{"phase":"Succeeded"}},{"status":{"phase":"Failed"}}]}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()
	k := kube{client: server.Client(), base: server.URL, token: "pod-token", namespace: "runners"}

	machines, err := k.Machines()
	if err != nil {
		t.Fatal(err)
	}
	want := machine{Name: "plot-dmz-100", Since: noon.Add(-20 * time.Minute), HeldBack: true, Jobs: 2, Boot: "b-1", Ready: true}
	if len(machines) != 1 || machines[0] != want {
		t.Fatalf("the machines were read as %+v, want %+v", machines, want)
	}
	if err := k.Mark("plot-dmz-100", nil, false); err != nil {
		t.Fatal(err)
	}
	if err := k.Mark("plot-dmz-100", &noon, true); err != nil {
		t.Fatal(err)
	}
	if err := k.Forget("plot-dmz-100"); err != nil {
		t.Fatal(err)
	}
	clean, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{inUseSince: nil}}, "spec": map[string]any{"unschedulable": false}})
	used, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{inUseSince: "2026-03-04T12:00:00Z"}}, "spec": map[string]any{"unschedulable": true}})
	wantAsked := []string{
		"GET /api/v1/nodes?labelSelector=" + zoneLabel,
		"GET /api/v1/namespaces/runners/pods?fieldSelector=spec.nodeName%3Dplot-dmz-100",
		"PATCH /api/v1/nodes/plot-dmz-100 " + string(clean),
		"PATCH /api/v1/nodes/plot-dmz-100 " + string(used),
		"DELETE /api/v1/nodes/plot-dmz-100",
	}
	if strings.Join(asked, "\n") != strings.Join(wantAsked, "\n") {
		t.Errorf("the cluster was asked:\n%s\nwant:\n%s", strings.Join(asked, "\n"), strings.Join(wantAsked, "\n"))
	}

	k.token = "somebody-else"
	if _, err := k.Machines(); err == nil {
		t.Error("a refusal was read as a cluster with no untrusted machines")
	}
}
