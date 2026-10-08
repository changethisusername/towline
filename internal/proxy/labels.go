package proxy

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// serviceLabels edits a compose service's labels in place, in either the
// mapping form or the "key=value" list form, keeping the order, style and
// comments of every entry it does not change. Entries are only ever
// rewritten individually; the labels block is never regenerated.
type serviceLabels struct {
	svc  *yaml.Node
	node *yaml.Node // the labels value; nil when the service has none
}

// editableLabels returns the labels of svc for editing, or an error when
// editing them in place would be unsafe: labels (or the service) shared
// with other nodes through YAML anchors and aliases, or labels inherited
// through a merge key (<<), which a local labels block would replace.
func editableLabels(root, svc *yaml.Node, service string) (*serviceLabels, error) {
	if svc.Kind == yaml.AliasNode {
		return nil, fmt.Errorf("service %q is a YAML alias (*%s); give it its own definition before changing its routing", service, svc.Value)
	}
	if svc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("service %q is not a mapping", service)
	}
	if svc.Anchor != "" && yamlIsAliased(root, svc) {
		return nil, fmt.Errorf("service %q is anchored (&%s) and reused by other services; changing its labels would change them too. Give it its own labels block outside the anchor first", service, svc.Anchor)
	}

	l := nodeLookup(svc, "labels")
	if l == nil || (l.Kind == yaml.ScalarNode && l.Tag == "!!null") {
		if yamlMergeHasKey(svc, "labels") {
			return nil, fmt.Errorf("service %q inherits its labels through a YAML merge key (<<); adding labels would replace the inherited ones. Give the service its own labels block first", service)
		}
		return &serviceLabels{svc: svc}, nil
	}

	switch {
	case l.Kind == yaml.AliasNode:
		return nil, fmt.Errorf("labels of service %q are a YAML alias (*%s) that may be shared with other services; give the service its own labels block first", service, l.Value)
	case l.Anchor != "" && yamlIsAliased(root, l):
		return nil, fmt.Errorf("labels of service %q are anchored (&%s) and reused elsewhere; give the service its own labels block first", service, l.Anchor)
	case l.Kind == yaml.MappingNode:
		for i := 0; i+1 < len(l.Content); i += 2 {
			k, v := l.Content[i], l.Content[i+1]
			if k.Value == "<<" || k.Tag == "!!merge" {
				return nil, fmt.Errorf("labels of service %q use a YAML merge key (<<); write them out explicitly first", service)
			}
			if v.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("label %q of service %q is not a plain value (YAML alias or nested node); write it out explicitly first", k.Value, service)
			}
		}
	case l.Kind == yaml.SequenceNode:
		for _, item := range l.Content {
			if item.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("labels of service %q contain an entry that is not a plain string (YAML alias or nested node); write it out explicitly first", service)
			}
		}
	default:
		return nil, fmt.Errorf("labels of service %q must be a mapping or a list", service)
	}
	return &serviceLabels{svc: svc, node: l}, nil
}

// splitLabel splits a list-form label. An entry without '=' is a label
// with an empty value.
func splitLabel(item string) (string, string) {
	k, v, _ := strings.Cut(item, "=")
	return k, v
}

// get returns the value of label key.
func (s *serviceLabels) get(key string) (string, bool) {
	if s.node == nil {
		return "", false
	}
	if s.node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(s.node.Content); i += 2 {
			if s.node.Content[i].Value == key {
				return s.node.Content[i+1].Value, true
			}
		}
		return "", false
	}
	for _, item := range s.node.Content {
		if k, v := splitLabel(item.Value); k == key {
			return v, true
		}
	}
	return "", false
}

// set updates label key in place, or appends it after the existing labels.
// A service without labels gets a new list-form labels block.
func (s *serviceLabels) set(key, value string) {
	if s.node == nil {
		s.node = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		replaced := false
		for i := 0; i+1 < len(s.svc.Content); i += 2 {
			if s.svc.Content[i].Value == "labels" {
				s.svc.Content[i+1] = s.node
				replaced = true
				break
			}
		}
		if !replaced {
			s.svc.Content = append(s.svc.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "labels"}, s.node)
		}
	}

	if s.node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(s.node.Content); i += 2 {
			if s.node.Content[i].Value == key {
				v := s.node.Content[i+1]
				v.Value, v.Tag = value, "!!str"
				return
			}
		}
		s.node.Content = append(s.node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
		return
	}

	entry := key + "=" + value
	for _, item := range s.node.Content {
		if k, _ := splitLabel(item.Value); k == key {
			item.Value, item.Tag = entry, "!!str"
			return
		}
	}
	s.node.Content = append(s.node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: entry})
}

// deleteMatching removes every label whose key satisfies match.
func (s *serviceLabels) deleteMatching(match func(key string) bool) {
	if s.node == nil {
		return
	}
	var kept []*yaml.Node
	if s.node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(s.node.Content); i += 2 {
			if !match(s.node.Content[i].Value) {
				kept = append(kept, s.node.Content[i], s.node.Content[i+1])
			}
		}
	} else {
		for _, item := range s.node.Content {
			if k, _ := splitLabel(item.Value); !match(k) {
				kept = append(kept, item)
			}
		}
	}
	s.node.Content = kept
}

// keys returns the label keys in document order.
func (s *serviceLabels) keys() []string {
	if s.node == nil {
		return nil
	}
	var out []string
	if s.node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(s.node.Content); i += 2 {
			out = append(out, s.node.Content[i].Value)
		}
		return out
	}
	for _, item := range s.node.Content {
		k, _ := splitLabel(item.Value)
		out = append(out, k)
	}
	return out
}

// dropIfEmpty removes the labels key from the service when no labels remain.
func (s *serviceLabels) dropIfEmpty() {
	if s.node != nil && len(s.node.Content) == 0 {
		removeNodeKey(s.svc, "labels")
		s.node = nil
	}
}

// yamlIsAliased reports whether any alias node under n refers to target.
func yamlIsAliased(n, target *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.AliasNode && n.Alias == target {
		return true
	}
	for _, c := range n.Content {
		if yamlIsAliased(c, target) {
			return true
		}
	}
	return false
}

// yamlMergeHasKey reports whether mapping inherits key through a merge key
// (<<), directly or through nested merges.
func yamlMergeHasKey(mapping *yaml.Node, key string) bool {
	return yamlMergeHasKeyDepth(mapping, key, 0)
}

func yamlMergeHasKeyDepth(mapping *yaml.Node, key string, depth int) bool {
	if mapping != nil && mapping.Kind == yaml.AliasNode {
		mapping = mapping.Alias
	}
	if mapping == nil || mapping.Kind != yaml.MappingNode || depth > 16 {
		return false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Value != "<<" && k.Tag != "!!merge" {
			continue
		}
		v := mapping.Content[i+1]
		sources := []*yaml.Node{v}
		if v.Kind == yaml.SequenceNode {
			sources = v.Content
		}
		for _, src := range sources {
			if src.Kind == yaml.AliasNode {
				src = src.Alias
			}
			if src == nil {
				continue
			}
			if nodeLookup(src, key) != nil || yamlMergeHasKeyDepth(src, key, depth+1) {
				return true
			}
		}
	}
	return false
}
