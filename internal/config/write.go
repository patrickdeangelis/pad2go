package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Save writes c to path, editing the existing YAML document in place so
// comments, key order and keys this program doesn't know (such as the
// original Switch2Connect's profiles) survive. A missing file starts from
// Sample. Per-mode overrides under button_remaps.xbox are updated too, so
// they don't shadow the saved top-level values on the next Load.
func (c *Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		data, err = []byte(Sample), nil
	}
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top level is not a mapping", path)
	}

	v := reflect.ValueOf(c).Elem()
	t := v.Type()
	values := map[string]any{}
	for i := range t.NumField() {
		key := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
		if key == "" || key == "-" || key == "button_remaps" {
			continue
		}
		values[key] = v.Field(i).Interface()
		if err := setKey(root, key, values[key]); err != nil {
			return err
		}
	}
	if remaps := lookup(lookup(root, "button_remaps"), "xbox"); remaps != nil && remaps.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(remaps.Content); i += 2 {
			if val, ok := values[remaps.Content[i].Value]; ok {
				if err := setKey(remaps, remaps.Content[i].Value, val); err != nil {
					return err
				}
			}
		}
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func lookup(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setKey replaces key's value in mapping m (appending the key if missing),
// keeping the existing value node's comments.
func setKey(m *yaml.Node, key string, value any) error {
	var n yaml.Node
	if err := n.Encode(value); err != nil {
		return fmt.Errorf("encode %s: %w", key, err)
	}
	if old := lookup(m, key); old != nil {
		n.HeadComment, n.LineComment, n.FootComment = old.HeadComment, old.LineComment, old.FootComment
		*old = n
		return nil
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &n)
	return nil
}
