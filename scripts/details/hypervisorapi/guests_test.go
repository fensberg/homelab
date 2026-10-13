package hypervisorapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hypervisor answers as one does: a list of machines, and for anything done
// to a machine a task's name at once and its outcome when asked. It records
// every request, and ends the tasks of the paths it is told to fail.
type hypervisor struct {
	asked []string
	fail  map[string]string
	looks int
}

func (h *hypervisor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "PVEAPIToken=who!what=value" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	_ = r.ParseForm()
	h.asked = append(h.asked, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+r.PostForm.Encode()))
	switch {
	case r.URL.Path == "/cluster/resources":
		fmt.Fprint(w, `{"data":[{"vmid":7300,"name":"plot-dmz-100","node":"host-a","status":"running","type":"qemu"}]}`)
	case strings.Contains(r.URL.Path, "/tasks/"):
		h.looks++
		if h.looks < 2 {
			fmt.Fprint(w, `{"data":{"status":"running"}}`)
			return
		}
		h.looks = 0
		exit := "OK"
		for path, ending := range h.fail {
			if strings.Contains(r.URL.Path, path) {
				exit = ending
			}
		}
		fmt.Fprintf(w, `{"data":{"status":"stopped","exitstatus":%q}}`, exit)
	default:
		// The task is named for what was asked, so its status can be failed
		// by what it was.
		fmt.Fprintf(w, `{"data":"UPID:%s"}`, strings.ReplaceAll(strings.Trim(r.URL.Path, "/"), "/", "-"))
	}
}

func session(t *testing.T, h *hypervisor) Guests {
	t.Helper()
	TaskPause = 0
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return Guests{Client: server.Client(), Base: server.URL, Auth: "PVEAPIToken=who!what=value"}
}

// What is done to a machine is done on the node that holds it, to that
// machine, and is finished only when the hypervisor says its task ended
// well: the power is cut and not asked for, the disk goes back to the clean
// snapshot and the machine is left stopped, and a deletion takes the disks.
func TestAMachineIsStoppedPutBackStartedAndDeletedByTasksThatAreWaitedFor(t *testing.T) {
	h := &hypervisor{}
	g := session(t, h)
	machines, err := g.List()
	if err != nil || len(machines) != 1 || machines[0].ID != 7300 || machines[0].Node != "host-a" || machines[0].Name != "plot-dmz-100" {
		t.Fatalf("the machines were read as %+v (%v)", machines, err)
	}
	m := machines[0]
	for _, do := range []func(Guest) error{g.Stop, g.PutBack, g.Start, g.Delete} {
		if err := do(m); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"GET /cluster/resources?type=vm",
		"POST /nodes/host-a/qemu/7300/status/stop",
		"POST /nodes/host-a/qemu/7300/snapshot/" + CleanSnapshot + "/rollback start=0",
		"POST /nodes/host-a/qemu/7300/status/start",
		"DELETE /nodes/host-a/qemu/7300?purge=1&destroy-unreferenced-disks=1",
	}
	var did []string
	for _, a := range h.asked {
		if !strings.Contains(a, "/tasks/") {
			did = append(did, a)
		}
	}
	if strings.Join(did, "\n") != strings.Join(want, "\n") {
		t.Errorf("the hypervisor was asked:\n%s\nwant:\n%s", strings.Join(did, "\n"), strings.Join(want, "\n"))
	}
	if looks := len(h.asked) - len(did); looks != 8 {
		t.Errorf("four tasks were looked at %d time(s); each is looked at until it has stopped, which here is twice", looks)
	}
}

// A task the hypervisor accepted and then failed is a failure, and so is
// one that never ends, and so is an answer that is not a yes.
func TestATaskThatEndsBadlyOrNeverEndsIsNotDone(t *testing.T) {
	h := &hypervisor{fail: map[string]string{"rollback": "zfs error: dataset is busy"}}
	g := session(t, h)
	m := Guest{ID: 7300, Node: "host-a"}
	if err := g.PutBack(m); err == nil || !strings.Contains(err.Error(), "dataset is busy") {
		t.Errorf("a rollback the hypervisor failed was taken as done: %v", err)
	}
	if err := g.Stop(m); err != nil {
		t.Errorf("a task that ended well beside one that did not was refused: %v", err)
	}

	looks := TaskLooks
	TaskLooks = 1
	t.Cleanup(func() { TaskLooks = looks })
	if err := g.Start(m); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Errorf("a task still running when the looking stopped was taken as done: %v", err)
	}

	g.Auth = "PVEAPIToken=somebody=else"
	if _, err := g.List(); err == nil {
		t.Error("a refusal was read as a hypervisor with no machines")
	}
	if err := g.Stop(m); err == nil {
		t.Error("a refusal was read as a machine that stopped")
	}
}
