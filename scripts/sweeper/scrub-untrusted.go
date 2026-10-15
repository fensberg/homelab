package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"homelab/details/capacity"
	"homelab/details/hypervisorapi"
)

// scrub-untrusted puts a untrusted worker back as it was built, once
// what ran on it has finished.
//
// WHY. Those machines run whatever a pull request says to run. A runner is
// made new for every job; the machine under it is not, so a job that got
// out of its container could wait there for the next one. The machine's
// disk is put back to the snapshot the converge took and the machine is
// started cold, so nothing a job left - on disk or in memory, the kernel
// included - is there for the next (docs/epochs/08-agent-roles.md).
//
// WHEN. As soon as a machine that has run something is running nothing.
// And a machine that never goes quiet is not left: once it has been in use
// for -most, it is held back from new jobs, the running ones finish, and it
// is put back then. No job is cut short.
//
// WHEN PUTTING BACK FAILS the machine is deleted, and its node with it. The
// sweeper does not look at why and does not try again: a machine that could
// not be put back is a machine nobody can vouch for. The next converge
// builds another.
//
// THIS IS NOT REFUSE, and it is the one verb here that is not safe to miss.
// It is the sweeper's because it is the same act - taking away what the
// work left - but it runs all the time, in the cluster, and when it is not
// running the machines go on being used without being put back. Nothing in
// the cluster expires by itself, so that is said by an alert and not held by
// a wall.
//
// WHAT IT HOLDS. A token on the hypervisor for the untrusted workers' pool
// alone, and in the cluster the right to read and mark those nodes, delete
// one, and see the runners' pods.

// inUseSince is where a machine's node says when it first ran something
// since it was last put back. On the node and not in this program, so that
// a sweeper that starts again knows what the last one knew, and so that it
// outlives the machine being put back: the node is the cluster's record,
// and is not on the machine's disk.
const inUseSince = capacity.UntrustedZone + "/in-use-since"

// untrustedLabel is the label every untrusted worker carries.
const untrustedLabel = capacity.UntrustedZone

// machine is one untrusted worker as the cluster sees it at one moment.
type machine struct {
	Name     string
	Since    time.Time // zero when nothing has run since it was put back
	HeldBack bool      // taking no new work
	Jobs     int       // runners on it that have not finished
	Boot     string    // changes every time the machine starts
	Ready    bool
}

type step int

const (
	wait step = iota
	markInUse
	holdBack
	putBack
)

// next is what to do about one machine now.
func next(m machine, now time.Time, most time.Duration) step {
	switch {
	case m.Since.IsZero() && m.Jobs > 0:
		return markInUse
	case m.Since.IsZero():
		return wait
	case m.Jobs == 0:
		return putBack
	case !m.HeldBack && now.Sub(m.Since) >= most:
		return holdBack
	}
	return wait
}

// cluster is what the sweeper asks of the cluster, and hypervisor what it
// asks of the hypervisor.
type cluster interface {
	Machines() ([]machine, error)
	Mark(name string, since *time.Time, heldBack bool) error
	Forget(name string) error
}

type hypervisor interface {
	List() ([]hypervisorapi.Guest, error)
	Stop(hypervisorapi.Guest) error
	PutBack(hypervisorapi.Guest) error
	Start(hypervisorapi.Guest) error
	Delete(hypervisorapi.Guest) error
}

type sweep struct {
	cluster    cluster
	hypervisor hypervisor
	most       time.Duration
	now        func() time.Time
	// How long a machine that was put back is given to come back, and how
	// often it is looked at.
	back  time.Duration
	pause time.Duration
	say   func(format string, args ...any)
}

// once looks at every untrusted worker and does what each needs.
func (s sweep) once() error {
	machines, err := s.cluster.Machines()
	if err != nil {
		return err
	}
	for _, m := range machines {
		switch next(m, s.now(), s.most) {
		case markInUse:
			at := s.now()
			if err := s.cluster.Mark(m.Name, &at, m.HeldBack); err != nil {
				return err
			}
			s.say("%s is in use", m.Name)
		case holdBack:
			if err := s.cluster.Mark(m.Name, &m.Since, true); err != nil {
				return err
			}
			s.say("%s has been in use for %s and takes no new work", m.Name, s.most)
		case putBack:
			if err := s.putBack(m); err != nil {
				return err
			}
		}
	}
	return nil
}

// putBack holds a machine back, makes sure nothing arrived while it did,
// and returns it to how it was built. A machine that cannot be returned is
// deleted.
func (s sweep) putBack(m machine) error {
	if err := s.cluster.Mark(m.Name, &m.Since, true); err != nil {
		return err
	}
	// A runner placed between the look and the mark is a job on a machine
	// that is about to lose its power. It is left to finish: the machine
	// stays held back and in use, and is put back when it is quiet.
	again, err := s.cluster.Machines()
	if err != nil {
		return err
	}
	for _, now := range again {
		if now.Name == m.Name && now.Jobs > 0 {
			s.say("%s took a job as it was being held back; waiting for it", m.Name)
			return nil
		}
	}

	guests, err := s.hypervisor.List()
	if err != nil {
		return err
	}
	var guest *hypervisorapi.Guest
	for i := range guests {
		if guests[i].Name == m.Name {
			guest = &guests[i]
		}
	}
	if guest == nil {
		return fmt.Errorf("the hypervisor shows this token no machine called %s, so there is nothing to put back; it stays held back", m.Name)
	}

	failed := func() error {
		if err := s.hypervisor.Stop(*guest); err != nil {
			return err
		}
		if err := s.hypervisor.PutBack(*guest); err != nil {
			return err
		}
		if err := s.hypervisor.Start(*guest); err != nil {
			return err
		}
		return s.cameBack(m)
	}()
	if failed == nil {
		if err := s.cluster.Mark(m.Name, nil, false); err != nil {
			return err
		}
		s.say("%s was put back as it was built", m.Name)
		return nil
	}

	s.say("%s could not be put back and is being deleted: %v", m.Name, failed)
	// Stopped first, whatever state the failure left it in; a machine that
	// is already stopped is not an error worth keeping it for.
	_ = s.hypervisor.Stop(*guest)
	if err := s.hypervisor.Delete(*guest); err != nil {
		return fmt.Errorf("%s could not be put back (%v) and could not be deleted: %w", m.Name, failed, err)
	}
	if err := s.cluster.Forget(m.Name); err != nil {
		return fmt.Errorf("%s was deleted and its node could not be removed: %w", m.Name, err)
	}
	s.say("%s is gone; the next converge builds another", m.Name)
	return nil
}

// cameBack waits for a machine to be ready again as a machine that has
// started since: ready alone is what the node went on saying while its
// machine was off.
func (s sweep) cameBack(m machine) error {
	deadline := s.now().Add(s.back)
	for {
		machines, err := s.cluster.Machines()
		if err != nil {
			return err
		}
		for _, now := range machines {
			if now.Name == m.Name && now.Ready && now.Boot != "" && now.Boot != m.Boot {
				return nil
			}
		}
		if !s.now().Before(deadline) {
			return fmt.Errorf("it did not come back ready within %s", s.back)
		}
		time.Sleep(s.pause)
	}
}

// --- the cluster, from inside it -------------------------------------------

// kube is the cluster's API as a pod reaches it: the address and the
// authority it was given, and its own account's token.
type kube struct {
	client *http.Client
	base   string
	token  string
	// Where the token is read from before every request, when it comes from
	// a file: the cluster renews it there, and one read once at the start
	// stops working within the hour.
	tokenFile string
	namespace string // where the runners are
}

func (k kube) call(method, path, contentType string, body []byte, into any) error {
	req, err := http.NewRequest(method, k.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	token := k.token
	if k.tokenFile != "" {
		raw, err := os.ReadFile(k.tokenFile)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(raw))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%s %s: the answer is not the shape this reads: %w", method, path, err)
	}
	return nil
}

// at walks an answer by its keys, and is nil where the path is not there.
func at(v any, keys ...string) any {
	for _, key := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}

func (k kube) Machines() ([]machine, error) {
	var nodes map[string]any
	if err := k.call(http.MethodGet, "/api/v1/nodes?labelSelector="+url.QueryEscape(untrustedLabel), "", nil, &nodes); err != nil {
		return nil, err
	}
	list, _ := at(nodes, "items").([]any)
	var out []machine
	for _, n := range list {
		m := machine{}
		m.Name, _ = at(n, "metadata", "name").(string)
		m.HeldBack, _ = at(n, "spec", capacity.Unschedulable).(bool)
		m.Boot, _ = at(n, "status", "nodeInfo", "bootID").(string)
		if m.Name == "" {
			return nil, errors.New("the cluster listed a node with no name")
		}
		conditions, _ := at(n, "status", "conditions").([]any)
		for _, c := range conditions {
			m.Ready = m.Ready || (at(c, "type") == "Ready" && at(c, "status") == "True")
		}
		if stamp, _ := at(n, "metadata", "annotations", inUseSince).(string); stamp != "" {
			since, err := time.Parse(time.RFC3339, stamp)
			if err != nil {
				return nil, fmt.Errorf("%s says it has been in use since %q, which is not a time; it is not this program that wrote that", m.Name, stamp)
			}
			m.Since = since
		}
		var pods map[string]any
		path := "/api/v1/namespaces/" + k.namespace + "/pods?fieldSelector=" + url.QueryEscape("spec.nodeName="+m.Name)
		if err := k.call(http.MethodGet, path, "", nil, &pods); err != nil {
			return nil, err
		}
		running, _ := at(pods, "items").([]any)
		for _, p := range running {
			if state := at(p, "status", "phase"); state != "Succeeded" && state != "Failed" {
				m.Jobs++
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func (k kube) Mark(name string, since *time.Time, heldBack bool) error {
	var at any
	if since != nil {
		at = since.UTC().Format(time.RFC3339)
	}
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"annotations": map[string]any{inUseSince: at}},
		"spec":     map[string]any{capacity.Unschedulable: heldBack},
	})
	if err != nil {
		return err
	}
	return k.call(http.MethodPatch, "/api/v1/nodes/"+name, "application/merge-patch+json", patch, nil)
}

func (k kube) Forget(name string) error {
	return k.call(http.MethodDelete, "/api/v1/nodes/"+name, "", nil, nil)
}

// account is where the cluster puts a pod's own credentials. A variable so
// that a test can put a pod's credentials somewhere else.
var account = "/var/run/secrets/kubernetes.io/serviceaccount"

func inCluster(namespace string) (kube, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return kube{}, errors.New("this is not running in a cluster: nothing says where its API is")
	}
	token, err := os.ReadFile(account + "/token")
	if err != nil {
		return kube{}, err
	}
	authority, err := os.ReadFile(account + "/ca.crt")
	if err != nil {
		return kube{}, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(authority) {
		return kube{}, errors.New("the cluster's authority does not hold a certificate")
	}
	return kube{
		client:    &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}},
		base:      "https://" + host + ":" + port,
		token:     strings.TrimSpace(string(token)),
		tokenFile: account + "/token",
		namespace: namespace,
	}, nil
}

func scrubUntrusted(args []string) int {
	fs := flag.NewFlagSet("scrub-untrusted", flag.ExitOnError)
	var (
		most      = fs.Duration("most", 30*time.Minute, "the longest a machine is in use before it is held back and put back")
		every     = fs.Duration("every", 15*time.Second, "how often every machine is looked at")
		namespace = fs.String("runners", envOr("RUNNERS_NAMESPACE", ""), "the namespace the runners' pods are in")
		listen    = fs.String("listen", ":8080", "where to answer whether it is still looking")
	)
	_ = fs.Parse(args)

	node, address := os.Getenv("HYPERVISOR_NODE"), os.Getenv("HYPERVISOR_URL")
	id, secret := os.Getenv("HYPERVISOR_TOKEN_ID"), os.Getenv("HYPERVISOR_TOKEN_SECRET")
	if *namespace == "" || node == "" || address == "" || id == "" || secret == "" {
		fatal("scrub-untrusted needs the runners' namespace, and the hypervisor's node, address and token, from its environment")
	}
	client, err := hypervisorapi.Client(os.Getenv("HYPERVISOR_AUTHORITY"), node, 30*time.Second)
	if err != nil {
		fatal(err.Error())
	}
	k, err := inCluster(*namespace)
	if err != nil {
		fatal(err.Error())
	}
	s := sweep{
		cluster:    k,
		hypervisor: hypervisorapi.Guests{Client: client, Base: address, TokenID: id, TokenSecret: secret},
		most:       *most,
		now:        time.Now,
		back:       5 * time.Minute,
		pause:      5 * time.Second,
		say: func(format string, args ...any) {
			fmt.Printf(time.Now().UTC().Format(time.RFC3339)+" "+format+"\n", args...)
		},
	}
	alive := &looking{now: time.Now, within: 3**every + s.back}
	go func() {
		server := &http.Server{Addr: *listen, Handler: alive, ReadHeaderTimeout: 5 * time.Second}
		fatal(server.ListenAndServe().Error())
	}()
	// Runs until it is stopped. A look that fails is said and the next look
	// is the same look: nothing is carried from one to the next but what the
	// cluster itself holds.
	for {
		if err := s.once(); err != nil {
			s.say("this look failed: %v", err)
		}
		alive.looked()
		time.Sleep(*every)
	}
}

// looking answers whether the sweeper is still looking: whether a look has
// finished, well or not, recently enough. A sweeper stuck inside one is not
// putting anything back, and is restarted for it. Whether the looks succeed
// is not asked here - a hypervisor that does not answer is not mended by
// restarting the program that asks it, and is said in its log.
type looking struct {
	mu     sync.Mutex
	last   time.Time
	now    func() time.Time
	within time.Duration
}

func (l *looking) looked() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.last = l.now()
}

func (l *looking) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last.IsZero() || l.now().Sub(l.last) > l.within {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}
