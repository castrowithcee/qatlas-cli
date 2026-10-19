package github

import (
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// toolEffects maps every tool of the provider to its effect.
var toolEffects = func() map[string]config.Effect {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		panic(err)
	}
	effects := map[string]config.Effect{}
	for _, descriptor := range reg.All() {
		effects[descriptor.ID] = config.Effect(descriptor.Risk.Effect)
	}
	return effects
}()

// connectionOf completes c with the selection of a connection. Without tools it offers by permissions. With
// tools it selects them alone, leaving out those whose effect permissions exclude, so a test can name the
// tools a connection with a narrower scope would never have offered.
func connectionOf(c config.Connection, permissions []config.Effect, tools []string) config.Connection {
	if tools == nil {
		c.Effects = permissions
		return c
	}
	c.Tools = []string{}
	for _, tool := range tools {
		for _, permission := range permissions {
			if toolEffects[tool] == permission {
				c.Tools = append(c.Tools, tool)
				break
			}
		}
	}
	return c
}
