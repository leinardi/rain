// Package cft provides the Template type that models a CloudFormation template.
//
// The sub-packages of cft contain various tools for working with templates
package cft

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Template represents a CloudFormation template. The Template type
// is minimal for now but will likely grow new features as needed by rain.
type Template struct {
	yaml.Node
}

// Map returns the template as a map[string]interface{}
func (t Template) Map() map[string]interface{} {
	var out map[string]interface{}

	err := t.Decode(&out)
	if err != nil {
		panic(fmt.Errorf("Error converting template to map: %s", err))
	}

	return out
}

// GetPath returns the yaml node that matches the provided path.
// If zero or more than one node matches the provided path,
// GetPath will return nil
func (t Template) GetPath(path string) *yaml.Node {
	results := make([]*yaml.Node, 0)
	for n := range t.MatchPath(path) {
		results = append(results, n)
	}

	if len(results) != 1 {
		return nil
	}

	return results[0]
}

// MatchPath returns all yaml nodes that match the provided path.
// The path is a `/`-separated string that describes a path into the template's tree.
// Wildcard elements (which can be map keys or array indices) are represented by a `*`.
// Matching an arbitrary number (including zero) of descendents can be done with `**`.
func (t Template) MatchPath(path string) <-chan *yaml.Node {
	ch := make(chan *yaml.Node)
	go func() {
		matchPath(ch, &t.Node, strings.Split(path, "/"))
		close(ch)
	}()

	return ch
}

func matchPath(ch chan<- *yaml.Node, n *yaml.Node, path []string) {
	if n.Kind == yaml.DocumentNode {
		for _, doc := range n.Content {
			matchPath(ch, doc, path)
		}
		return
	}

	if len(path) == 0 {
		ch <- n
		return
	}

	head, tail := path[0], path[1:]
	query := make([]string, 0)

	// Deal with recursive descent
	if head == "**" {
		matchPath(ch, n, tail)

		if n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				matchPath(ch, n.Content[i+1], path)
			}
		} else if n.Kind == yaml.SequenceNode {
			for _, child := range n.Content {
				matchPath(ch, child, path)
			}
		}
	}

	// Parse out any query
	parts := strings.Split(head, "|")
	if len(parts) == 2 {
		head = parts[0]
		query = parts[1:]
	}

	if n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]

			if head == "*" || key.Value == head {
				value := n.Content[i+1]
				if filter(value, query) {
					matchPath(ch, value, tail)
				}
			}
		}
	} else if n.Kind == yaml.SequenceNode {
		if head == "*" {
			for _, child := range n.Content {
				if filter(child, query) {
					matchPath(ch, child, tail)
				}
			}
		} else {
			i, err := strconv.Atoi(head)
			if err == nil && i < len(n.Content) {
				value := n.Content[i]
				if filter(value, query) {
					matchPath(ch, value, tail)
				}
			}
		}
	}
}

func filter(n *yaml.Node, query []string) bool {
	for _, q := range query {
		parts := strings.Split(q, "==")

		var value *yaml.Node
		if n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				if n.Content[i].Value == parts[0] {
					value = n.Content[i+1]
					break
				}
			}

			if value == nil {
				return false
			}
		} else if n.Kind == yaml.SequenceNode {
			i, err := strconv.Atoi(parts[0])
			if err != nil || i >= len(n.Content) {
				return false
			}
			value = n.Content[i]
		} else {
			return false
		}

		if len(parts) == 2 && value.Value != parts[1] {
			if value.Kind != yaml.ScalarNode {
				return false
			}

			return false
		}
	}

	return true
}
