package harness

import (
	"gopkg.in/yaml.v3"
)

// RunnersOf is every runner a job's runs-on can ask for, as a job asks for
// it: the runner it names, or each runner a list names. An expression is a
// runner nothing here can name, and is read as one.
func RunnersOf(n *yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
		return out
	}
	return nil
}
