module homelab/engineer

go 1.26

// The engineer holds no key and changes nothing: it reads what is declared
// and says what a site can carry. That is why it may read YAML with a
// library where the programs that hold power may not - the worst a bad
// dependency can do here is a wrong answer on a pull request.
require (
	gopkg.in/yaml.v3 v3.0.1
	homelab/details v0.0.0
)

replace homelab/details => ../details
