package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnvTag is the YAML tag that injects a value from an environment variable:
//
//	password: !env SMTP_PASSWORD
//
// A missing environment variable fails startup rather than silently yielding
// an empty credential.
const EnvTag = "!env"

// expandEnv walks the YAML node tree and replaces every node tagged !env with
// the value of the named environment variable.
//
// It runs before decoding, so it works uniformly for typed struct fields and
// for free-form maps such as a channel's config block — no field has to opt in
// by adopting a special type.
func expandEnv(n *yaml.Node) error {
	if n == nil {
		return nil
	}

	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if err := expandEnv(c); err != nil {
				return err
			}
		}

	case yaml.MappingNode:
		// Content is [key, value, key, value, ...]. Keys are never expanded.
		for i := 0; i+1 < len(n.Content); i += 2 {
			if err := expandEnv(n.Content[i+1]); err != nil {
				return err
			}
		}

	case yaml.AliasNode:
		return expandEnv(n.Alias)

	case yaml.ScalarNode:
		if n.Tag != EnvTag {
			return nil
		}
		name := strings.TrimSpace(n.Value)
		if name == "" {
			return fmt.Errorf("config: %s on line %d needs an environment variable name", EnvTag, n.Line)
		}
		val, ok := os.LookupEnv(name)
		if !ok {
			return fmt.Errorf("config: %s %s (line %d) referenced but the environment variable is not set",
				EnvTag, name, n.Line)
		}
		n.Tag = "!!str"
		n.Value = val
	}

	return nil
}
