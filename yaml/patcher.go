package yaml

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vmware-labs/yaml-jsonpath/pkg/yamlpath"
	goyaml "gopkg.in/yaml.v3"
)

type Patcher struct {
	node *goyaml.Node
}

func NewPatcher(r io.Reader) (*Patcher, error) {
	dec := goyaml.NewDecoder(r)
	var node goyaml.Node
	if err := dec.Decode(&node); err != nil {
		return nil, err
	}

	return &Patcher{
		node: &node,
	}, nil
}

func (p *Patcher) SetField(path string, value any, createKeys bool) error {
	parsedPath, err := yamlpath.NewPath(path)
	if err != nil {
		return fmt.Errorf("parsing path: %w", err)
	}

	matchedNodes, err := parsedPath.Find(p.node)
	if err != nil {
		return fmt.Errorf("finding value node: %w", err)
	}

	var valueNode *goyaml.Node

	if len(matchedNodes) == 0 {
		if createKeys {
			pathParts := strings.Split(path, ".")
			// Note: we do not support JSONPath expressions in the path if createKeys is executed!
			valueNode, err = recurseNodeByPath(p.node, pathParts, true)
			if err != nil {
				return fmt.Errorf("creating path: %w", err)
			}
		} else {
			return errors.New("no nodes matched path")
		}
	} else if len(matchedNodes) > 1 {
		return errors.New("multiple nodes matched path")
	} else {
		valueNode = matchedNodes[0]
	}

	if valueNode.Kind != goyaml.ScalarNode {
		return fmt.Errorf("expected scalar node, got %s (at %d:%d)", kindToStr(valueNode.Kind), valueNode.Line, valueNode.Column)
	}

	err = valueNode.Encode(value)
	if err != nil {
		return fmt.Errorf("encoding value: %w", err)
	}

	return nil
}

func recurseNodeByPath(node *goyaml.Node, path []string, createKeys bool) (valueNode *goyaml.Node, err error) {
	if node.Kind == goyaml.DocumentNode {
		return handleDocumentNode(node, path, createKeys)
	}

	if len(path) == 0 {
		return handleScalarNode(node)
	}

	if node.Kind == goyaml.MappingNode {
		return handleMappingNode(node, path, createKeys)
	}

	return nil, fmt.Errorf("unexpected node of kind %s (at %d:%d)", kindToStr(node.Kind), node.Line, node.Column)
}

func handleDocumentNode(node *goyaml.Node, path []string, createKeys bool) (*goyaml.Node, error) {
	if len(node.Content) != 1 {
		return nil, fmt.Errorf("expected exactly one node in document, got %d (at %d:%d)", len(node.Content), node.Line, node.Column)
	}

	// Special case for empty documents
	if createKeys && node.Content[0].Kind == goyaml.ScalarNode && node.Content[0].Tag == "!!null" {
		// The document is empty, so we need to create a mapping node
		node.Content[0] = &goyaml.Node{
			Kind: goyaml.MappingNode,
		}
	}

	return recurseNodeByPath(node.Content[0], path, createKeys)
}

func handleScalarNode(node *goyaml.Node) (*goyaml.Node, error) {
	if node.Kind != goyaml.ScalarNode {
		return nil, fmt.Errorf("expected scalar node, got %s (at %d:%d)", kindToStr(node.Kind), node.Line, node.Column)
	}

	return node, nil
}

func handleMappingNode(node *goyaml.Node, path []string, createKeys bool) (*goyaml.Node, error) {
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if key == path[0] {
			return recurseNodeByPath(node.Content[i+1], path[1:], createKeys)
		}
	}

	// We didn't find the key, so we need to create it
	if createKeys {
		keyNode := &goyaml.Node{
			Kind:  goyaml.ScalarNode,
			Value: path[0],
		}
		// Create a mapping node if the path is longer than 1
		if len(path) > 1 {
			mappingNode := &goyaml.Node{
				Kind: goyaml.MappingNode,
			}
			node.Content = append(node.Content, keyNode, mappingNode)
			return recurseNodeByPath(mappingNode, path[1:], createKeys)
		}

		// Otherwise, create a scalar node
		scalarNode := &goyaml.Node{
			Kind: goyaml.ScalarNode,
		}
		node.Content = append(node.Content, keyNode, scalarNode)
		return scalarNode, nil
	}

	return node, fmt.Errorf("key %q not found (at %d:%d)", path[0], node.Line, node.Column)
}

func kindToStr(kind goyaml.Kind) string {
	switch kind {
	case goyaml.DocumentNode:
		return "DocumentNode"
	case goyaml.SequenceNode:
		return "SequenceNode"
	case goyaml.MappingNode:
		return "MappingNode"
	case goyaml.ScalarNode:
		return "ScalarNode"
	case goyaml.AliasNode:
		return "AliasNode"
	default:
		return fmt.Sprintf("unknown kind: %d", kind)
	}
}

func (p *Patcher) Encode(w io.Writer) error {
	enc := goyaml.NewEncoder(w)
	enc.SetIndent(2)
	return enc.Encode(p.node)
}
