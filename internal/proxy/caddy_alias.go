package proxy

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// CaddyUpstreamHost is the network alias that Caddy routes dial for a
// service. Docker DNS resolves a bare service name to every container with
// that name or alias on the network, and Caddy's network is shared between
// projects, so a bare name can send one project's traffic to another
// project's "web". Routed service names and stack names cannot contain '.', so
// this name cannot be produced by any other (stack, service) pair, and the
// compose policy stops other stacks from claiming it as a service name,
// container name or alias.
func CaddyUpstreamHost(stackName, service string) string {
	return service + "." + stackName + ".towline"
}

// withNetworkAlias returns composeContent with alias added to the service's
// aliases on every network the service is attached to (the default network
// when it lists none). It is returned unchanged if every network already
// has the alias. Network definitions shared through YAML anchors, aliases
// or merge keys are refused rather than rewritten.
func withNetworkAlias(composeContent, service, alias string) (string, error) {
	doc, svc, err := findServiceNode(composeContent, service)
	if err != nil {
		return "", err
	}
	root := doc.Content[0]

	if svc.Kind == yaml.AliasNode {
		return "", fmt.Errorf("service %q is a YAML alias (*%s); give it its own definition before routing it", service, svc.Value)
	}
	if svc.Kind != yaml.MappingNode {
		return "", fmt.Errorf("service %q is not a mapping", service)
	}
	if svc.Anchor != "" && yamlIsAliased(root, svc) {
		return "", fmt.Errorf("service %q is anchored (&%s) and reused by other services; give it its own networks block outside the anchor before routing it", service, svc.Anchor)
	}
	if nodeLookup(svc, "network_mode") != nil {
		return "", fmt.Errorf("service %q uses network_mode, so it cannot be given the network alias %q that Caddy routes to", service, alias)
	}

	changed := false
	nets := nodeLookup(svc, "networks")
	switch {
	case nets == nil || (nets.Kind == yaml.ScalarNode && nets.Tag == "!!null"):
		if yamlMergeHasKey(svc, "networks") {
			return "", fmt.Errorf("service %q inherits its networks through a YAML merge key (<<); give the service its own networks block before routing it", service)
		}
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		m.Content = append(m.Content, scalarNode("default"), aliasesNode(alias))
		setNodeKey(svc, "networks", m)
		changed = true

	case nets.Kind == yaml.AliasNode:
		return "", fmt.Errorf("networks of service %q are a YAML alias (*%s); give the service its own networks block before routing it", service, nets.Value)

	case nets.Anchor != "" && yamlIsAliased(root, nets):
		return "", fmt.Errorf("networks of service %q are anchored (&%s) and reused elsewhere; give the service its own networks block before routing it", service, nets.Anchor)

	case nets.Kind == yaml.SequenceNode:
		// Short form: convert to the long form, which can carry aliases.
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: nets.HeadComment, LineComment: nets.LineComment}
		for _, item := range nets.Content {
			if item.Kind != yaml.ScalarNode {
				return "", fmt.Errorf("networks of service %q contain an entry that is not a plain network name", service)
			}
			m.Content = append(m.Content, scalarNode(item.Value), aliasesNode(alias))
		}
		setNodeKey(svc, "networks", m)
		changed = true

	case nets.Kind == yaml.MappingNode:
		for i := 0; i+1 < len(nets.Content); i += 2 {
			name, cfg := nets.Content[i], nets.Content[i+1]
			if name.Value == "<<" || name.Tag == "!!merge" {
				return "", fmt.Errorf("networks of service %q use a YAML merge key (<<); write them out explicitly before routing it", service)
			}
			switch {
			case cfg.Kind == yaml.ScalarNode && (cfg.Tag == "!!null" || cfg.Value == ""):
				nets.Content[i+1] = aliasesNode(alias)
				changed = true
			case cfg.Kind == yaml.MappingNode && !(cfg.Anchor != "" && yamlIsAliased(root, cfg)):
				added, err := addAlias(cfg, alias)
				if err != nil {
					return "", fmt.Errorf("network %q of service %q: %w", name.Value, service, err)
				}
				changed = changed || added
			default:
				return "", fmt.Errorf("network %q of service %q is shared through a YAML anchor or alias, or is not a mapping; write it out explicitly before routing it", name.Value, service)
			}
		}

	default:
		return "", fmt.Errorf("networks of service %q must be a list or a mapping", service)
	}

	if !changed {
		return composeContent, nil
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal compose file: %w", err)
	}
	return string(out), nil
}

// addAlias appends alias to a network's aliases list, reporting whether it
// was added (false when already present).
func addAlias(cfg *yaml.Node, alias string) (bool, error) {
	for i := 0; i+1 < len(cfg.Content); i += 2 {
		if cfg.Content[i].Value == "<<" || cfg.Content[i].Tag == "!!merge" {
			return false, fmt.Errorf("uses a YAML merge key (<<); write it out explicitly")
		}
	}
	aliases := nodeLookup(cfg, "aliases")
	if aliases == nil || (aliases.Kind == yaml.ScalarNode && aliases.Tag == "!!null") {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{scalarNode(alias)}}
		setNodeKey(cfg, "aliases", seq)
		return true, nil
	}
	if aliases.Kind != yaml.SequenceNode || aliases.Anchor != "" {
		return false, fmt.Errorf("aliases must be a plain list")
	}
	for _, a := range aliases.Content {
		if a.Kind != yaml.ScalarNode {
			return false, fmt.Errorf("aliases must be a plain list")
		}
		if a.Value == alias {
			return false, nil
		}
	}
	aliases.Content = append(aliases.Content, scalarNode(alias))
	return true, nil
}

func scalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// aliasesNode builds the network config {aliases: [alias]}.
func aliasesNode(alias string) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		scalarNode("aliases"),
		{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{scalarNode(alias)}},
	}}
}

// setNodeKey sets key in mapping to value, replacing an existing value in
// place or appending the pair.
func setNodeKey(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, scalarNode(key), value)
}
