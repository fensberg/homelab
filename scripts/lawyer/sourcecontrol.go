package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"homelab/details/console"
	"homelab/details/ghapi"
	"homelab/details/tofustate"
)

// adoptionsFile is where a run writes which standing things the estate takes
// as its own. Beside the root for as long as the run lasts, and never in git.
const adoptionsFile = "adopted.tf"

// sourceControlBase is a variable so the tests can point it at a local server.
var sourceControlBase = ""

// sourceControl is the estate's organisation at its source control, asked
// with the estate's own App.
type sourceControl struct {
	owner string
	app   ghapi.App
}

// sourceControl reads who to ask and as whom out of the config. The key is
// kept as the vault keeps a key of more than one line: encoded onto one.
func (c config) sourceControl() (sourceControl, error) {
	parts := strings.Split(strings.TrimSuffix(c.SourceControl.URL, ".git"), "/")
	if len(parts) != 5 || parts[3] == "" || parts[4] == "" {
		return sourceControl{}, errors.New("the address of the estate's repository, in the estate-shared vault, is not https://<host>/<organisation>/<repository>")
	}
	app, err := c.SourceControl.App(sourceControlBase)
	if err != nil {
		return sourceControl{}, fmt.Errorf("source-control/private_key in the estate vault: %w", err)
	}
	return sourceControl{owner: parts[3], app: app}, nil
}

// rulesets is the number of every ruleset a repository holds, by its name.
// Two of one name is refused: nothing then says which the estate means.
func (s sourceControl) rulesets(repository string) (map[string]int64, error) {
	token, err := s.app.Token(time.Now())
	if err != nil {
		return nil, fmt.Errorf("signing in as the estate's App: %w", err)
	}
	listed, err := ghapi.Get[[]struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}](sourceControlBase, token, fmt.Sprintf("/repos/%s/%s/rulesets?per_page=100&includes_parents=false", s.owner, repository))
	if err != nil {
		return nil, fmt.Errorf("listing the rulesets of %s: %w. The estate's App may lack the permission to administer the repository", repository, err)
	}
	out := map[string]int64{}
	for _, r := range listed {
		if _, twice := out[r.Name]; twice {
			return nil, fmt.Errorf("%s holds two rulesets named %q, so nothing says which is the estate's. Remove one by hand", repository, r.Name)
		}
		out[r.Name] = r.ID
	}
	return out, nil
}

// organisation is the platform's number for the estate's organisation.
func (s sourceControl) organisation() (int64, error) {
	token, err := s.app.Token(time.Now())
	if err != nil {
		return 0, fmt.Errorf("signing in as the estate's App: %w", err)
	}
	org, err := ghapi.Get[struct {
		ID int64 `json:"id"`
	}](sourceControlBase, token, "/orgs/"+s.owner)
	if err != nil {
		return 0, fmt.Errorf("asking after the estate's organisation: %w", err)
	}
	if org.ID == 0 {
		return 0, errors.New("the source control answered for the estate's organisation without its number")
	}
	return org.ID, nil
}

// standing is what the estate asks its source control about what already
// stands there.
type standing interface {
	rulesets(repository string) (map[string]int64, error)
	organisation() (int64, error)
}

// wantedRuleset is one ruleset a plan would make.
type wantedRuleset struct {
	address, repository, name string
}

// plannedRulesets is every ruleset the plan would create, sorted by address.
func plannedRulesets(showJSON []byte) ([]wantedRuleset, error) {
	p, err := tofustate.ParsePlan(showJSON)
	if err != nil {
		return nil, fmt.Errorf("reading the plan: %w", err)
	}
	var out []wantedRuleset
	for _, rc := range p.ResourceChanges {
		if rc.Type != ghapi.RulesetType || !rc.Creates() {
			continue
		}
		name, named := rc.AfterString("name")
		repository, placed := rc.AfterString("repository")
		if !named || !placed || name == "" || repository == "" {
			return nil, fmt.Errorf("the plan creates %s without a known name and repository, so it cannot be matched to one that already stands", rc.Address)
		}
		out = append(out, wantedRuleset{rc.Address, repository, name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].address < out[j].address })
	return out, nil
}

// plannedOrganisations is the address of every set of organisation settings
// the plan would create.
func plannedOrganisations(showJSON []byte) ([]string, error) {
	p, err := tofustate.ParsePlan(showJSON)
	if err != nil {
		return nil, fmt.Errorf("reading the plan: %w", err)
	}
	var out []string
	for _, rc := range p.ResourceChanges {
		if rc.Type == ghapi.OrganisationSettingsType && rc.Creates() {
			out = append(out, rc.Address)
		}
	}
	sort.Strings(out)
	return out, nil
}

// adoptSourceControl has the estate take as its own what it declares at its
// source control that already stands there: every ruleset the repository
// holds under the same name, and the organisation's settings.
//
// THE ORGANISATION'S SETTINGS ARE ALWAYS ADOPTED AND NEVER MADE. They exist
// for as long as the organisation does, and making them writes every field
// the resource has - the ones nobody declared as empty. Adopted, the estate
// starts from what stands and changes what it declares.
//
// The rules on a repository's branches outlive the estate's machinery, as
// its buckets do: they were there before any of it and a demolish releases
// them. So a build finds them standing, and making one again would leave two.
//
// It writes import blocks beside the root for this run, and does not import
// by command: an import by command is a change to the estate's record, and
// plan-estate changes nothing. Written this way a plan shows the adoption
// and anything a converge would then alter in what it adopted, and only an
// apply records it. They are written here and not in git because which
// ruleset is which is the platform's number for it, found by asking; and
// because the estate's own tests cannot run with an import in the root.
func (e *estate) adoptSourceControl(there standing) error {
	file := filepath.Join(e.dir, adoptionsFile)
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	shown, err := e.shownPlan("adopt.tfplan")
	if err != nil {
		return err
	}
	wanted, err := plannedRulesets([]byte(shown))
	if err != nil {
		return err
	}
	organisations, err := plannedOrganisations([]byte(shown))
	if err != nil {
		return err
	}
	held := map[string]map[string]int64{}
	var blocks strings.Builder
	for _, w := range wanted {
		if _, asked := held[w.repository]; !asked {
			if held[w.repository], err = there.rulesets(w.repository); err != nil {
				return err
			}
		}
		id, stands := held[w.repository][w.name]
		if !stands {
			continue
		}
		console.Info("adopting a ruleset that already stands: " + w.address)
		fmt.Fprintf(&blocks, "import {\n  to = %s\n  id = %s\n}\n", w.address, strconv.Quote(w.repository+":"+strconv.FormatInt(id, 10)))
	}
	for _, address := range organisations {
		id, err := there.organisation()
		if err != nil {
			return err
		}
		console.Info("adopting the organisation's settings as they stand: " + address)
		fmt.Fprintf(&blocks, "import {\n  to = %s\n  id = %s\n}\n", address, strconv.Quote(strconv.FormatInt(id, 10)))
	}
	if blocks.Len() == 0 {
		return nil
	}
	return os.WriteFile(file, []byte(blocks.String()), 0o600)
}

// outlivingTheEstate picks out of a state listing what a demolish must not
// reach: every bucket, and everything set at the source control. The
// repository is where the estate is declared, and its walls stand whether
// or not an estate does; the next build adopts them back.
func outlivingTheEstate(listed []string) []string {
	out := bucketAddresses(listed)
	for _, a := range listed {
		// What the estate only reads there is nothing it holds.
		if strings.HasPrefix(a, "data.") || strings.Contains(a, ".data.") {
			continue
		}
		if strings.HasPrefix(a, "github_") || strings.Contains(a, ".github_") {
			out = append(out, a)
		}
	}
	return out
}
