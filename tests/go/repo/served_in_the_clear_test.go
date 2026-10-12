package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Nothing a device reaches is served in the clear.
//
// What a device reaches is a Service that sets its own address: the estate
// routes an enrolled device to that address and to no other, so those
// Services are the whole of what is served to anything outside the cluster.
// Each port of one is held to one of two things. Over TCP it is HTTPS, and
// says so in its name, which is the one place a Service can say it. Over
// UDP it speaks a protocol of its own that no certificate is served on, and
// it is named here as outside this, by the test's own output, and not passed
// in silence.
//
// What this cannot see is whether the pods behind a port named for HTTPS
// serve it. That is a fact about a running site and is asked of one nightly.
// What the cluster's own parts say to each other is not a device reaching
// anything, and is the network's to encrypt
// (docs/epochs/11-black-box-white-glove.md).

// servedPort is one port of a Service that sets its own address.
type servedPort struct{ file, service, name, protocol string }

// servedPorts is every port of every Service in one manifest that sets its
// own address.
func servedPorts(rel, body string) []servedPort {
	var out []servedPort
	dec := yaml.NewDecoder(strings.NewReader(body))
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				ClusterIP string `yaml:"clusterIP"`
				Ports     []struct {
					Name     string `yaml:"name"`
					Protocol string `yaml:"protocol"`
				} `yaml:"ports"`
			} `yaml:"spec"`
		}
		// A document that is not YAML this reads is not a Service: Helm
		// values and vendored manifests are in the same trees.
		if dec.Decode(&doc) != nil {
			break
		}
		if doc.Kind != "Service" || doc.Spec.ClusterIP == "" || doc.Spec.ClusterIP == "None" {
			continue
		}
		if len(doc.Spec.Ports) == 0 {
			out = append(out, servedPort{rel, doc.Metadata.Name, "", ""})
		}
		for _, p := range doc.Spec.Ports {
			out = append(out, servedPort{rel, doc.Metadata.Name, p.Name, p.Protocol})
		}
	}
	return out
}

// inTheClear sorts what is served to a device into what is refused and what
// is outside the rule, each said in words.
func inTheClear(ports []servedPort) (refused, outside []string) {
	for _, p := range ports {
		switch {
		case p.protocol == "UDP":
			outside = append(outside, fmt.Sprintf("%s: the Service %s serves %q over UDP, a protocol of its own that no certificate is served on.", p.file, p.service, p.name))
		// A port that names no protocol is TCP: that is what Kubernetes
		// takes it for.
		case p.name != "https":
			refused = append(refused, fmt.Sprintf("%s: the Service %s is reached by a device and its port %q is not served over HTTPS.\n\n"+
				"Nothing a device reaches is served in the clear. Serve the port with the certificate the site is handed and name it https, "+
				"which is how a Service says so.", p.file, p.service, p.name))
		}
	}
	sort.Strings(refused)
	sort.Strings(outside)
	return refused, outside
}

func TestNothingADeviceReachesIsServedInTheClear(t *testing.T) {
	root := repoRoot(t)
	var ports []servedPort
	for _, rel := range tracked(t, func(rel string) bool {
		return (strings.HasPrefix(rel, fluxTree+"/") || strings.HasPrefix(rel, "modules/")) &&
			(strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml"))
	}) {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		ports = append(ports, servedPorts(rel, string(data))...)
	}
	// No floor on how many there are: a site with no route serves nothing to
	// a device. What this refuses is proved against Services written below.
	refused, outside := inTheClear(ports)
	for _, each := range outside {
		t.Log("outside this rule - " + each)
	}
	for _, each := range refused {
		t.Error(each)
	}
}

func TestInTheClearRefusesEveryPortThatIsNotHTTPSAndNamesWhatIsOutsideIt(t *testing.T) {
	read := servedPorts("f.yaml", strings.Join([]string{
		"kind: Service\nmetadata: {name: allocated}\nspec: {ports: [{name: http}]}",
		"kind: Service\nmetadata: {name: headless}\nspec: {clusterIP: None, ports: [{name: http}]}",
		"kind: Service\nmetadata: {name: front-door}\nspec: {clusterIP: \"${ADDRESS_FRONT_DOOR}\", ports: [{name: https, protocol: TCP}, {name: plain}, {name: own, protocol: UDP}]}",
		"kind: Service\nmetadata: {name: side-door}\nspec: {clusterIP: \"${ADDRESS_SIDE_DOOR}\"}",
		"kind: ConfigMap\nmetadata: {name: other}\nspec: {clusterIP: 1.2.3.4, ports: [{name: http}]}",
	}, "\n---\n"))
	want := []servedPort{
		{"f.yaml", "front-door", "https", "TCP"},
		{"f.yaml", "front-door", "plain", ""},
		{"f.yaml", "front-door", "own", "UDP"},
		{"f.yaml", "side-door", "", ""},
	}
	if fmt.Sprint(read) != fmt.Sprint(want) {
		t.Fatalf("what is served to a device was read as %+v, and is %+v", read, want)
	}

	refused, outside := inTheClear(read)
	if len(refused) != 2 || !strings.Contains(refused[0], `front-door is reached by a device and its port "plain"`) ||
		!strings.Contains(refused[1], `side-door is reached by a device and its port ""`) {
		t.Errorf("a TCP port not named for HTTPS, and a Service that names no port, are each refused; got %v", refused)
	}
	if len(outside) != 1 || !strings.Contains(outside[0], `front-door serves "own" over UDP`) {
		t.Errorf("a UDP port is named as outside the rule and not passed in silence; got %v", outside)
	}
	// A port over TCP that only claims the name in another case, or a
	// protocol, is not one served over HTTPS.
	if refused, _ := inTheClear([]servedPort{{"f.yaml", "x", "HTTPS", "TCP"}, {"f.yaml", "x", "http", "SCTP"}}); len(refused) != 2 {
		t.Errorf("only a port named https is taken as served over it; got %v", refused)
	}
}
