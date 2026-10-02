package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"homelab/details/applications"
	"homelab/details/repopath"
)

// upstream is one application the expediter keeps on what its supplier
// ships: where to ask, and the one line it may move when the answer changes.
type upstream struct {
	// The application, which names the branch and the pull request.
	Name string `json:"name"`
	// What to ask the supplier about, and where it announces a new build.
	App  string `json:"app"`
	News string `json:"news"`
	// The line that records the build, and the file it is in.
	Pin  string `json:"pin"`
	Pins string `json:"pins"`
}

// upstreams is every application that declares an upstream, in name order.
//
// The expediter knows no application. Each one that wants to be kept on its
// supplier's build says so in its own declaration - which supplier, what to
// ask it about, and which line of its own pins file records the answer - and
// the workflow expedites whatever this lists. An application with no
// upstream is not listed, and one whose declaration does not read fails the
// run rather than being passed over.
func upstreams(repoRoot string) ([]upstream, error) {
	apps, err := applications.Read(repoRoot)
	if err != nil {
		return nil, err
	}
	out := []upstream{}
	for _, a := range apps {
		if u := a.Upstream; u != nil {
			out = append(out, upstream{Name: a.Name, App: u.App, News: u.News, Pin: u.Pin, Pins: a.PinsFile()})
		}
	}
	return out, nil
}

func expediteUpstreams(args []string) int {
	fs := flag.NewFlagSet("expedite-upstreams", flag.ContinueOnError)
	root := fs.String("root", "", "the repository to read (default: this one)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *root == "" {
		found, err := repopath.Root()
		if err != nil {
			fmt.Fprintln(os.Stderr, "procurement expedite-upstreams:", err)
			return 1
		}
		*root = found
	}
	list, err := upstreams(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "procurement expedite-upstreams:", err)
		return 1
	}
	body, err := json.Marshal(list)
	if err != nil {
		fmt.Fprintln(os.Stderr, "procurement expedite-upstreams:", err)
		return 1
	}
	fmt.Println(string(body))
	return 0
}
