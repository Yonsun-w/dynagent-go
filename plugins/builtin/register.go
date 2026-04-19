package builtin

import (
	"github.com/admin/ai_project/internal/node"
)

func RegisterAll(registry *node.Registry) error {
	for _, builtIn := range []node.Node{
		intentParseNode{},
		textTransformNode{},
		genericHTTPCallNode{},
		finalizeNode{},
	} {
		if err := registry.RegisterBuiltin(builtIn); err != nil {
			return err
		}
	}
	return nil
}
