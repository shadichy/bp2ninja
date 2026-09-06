package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"plugin"

	"bp2ninja/pkg/ninja"
)

// PluginContext provides context and utility helpers to custom module plugins.
type PluginContext struct {
	ModuleName       string
	ModuleType       string
	Properties       map[string]interface{}
	TopDir           string
	OutDir           string
	BpDir            string
	AllowMissingDeps bool
	NinjaWriter      *ninja.Writer
}

func (c *PluginContext) EmitPhonyIfNeeded(target string) {
	if !c.AllowMissingDeps {
		return
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		_ = c.NinjaWriter.Build(ninja.BuildEdge{
			Outputs: []string{target},
			Rule:    "phony",
		})
	}
}

func (c *PluginContext) ResolvePath(p string) string {
	if !filepath.IsAbs(p) && c.BpDir != "" {
		return filepath.Join(c.BpDir, p)
	}
	return p
}

func (c *PluginContext) GetString(prop string) string {
	if val, ok := c.Properties[prop]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}

func (c *PluginContext) GetBool(prop string) bool {
	if val, ok := c.Properties[prop]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

func (c *PluginContext) GetStringList(prop string) []string {
	var res []string
	val, ok := c.Properties[prop]
	if !ok {
		return res
	}

	if list, ok := val.([]interface{}); ok {
		for _, item := range list {
			if s, ok := item.(string); ok {
				res = append(res, s)
			}
		}
	} else if s, ok := val.(string); ok {
		res = append(res, s)
	}
	return res
}

func (c *PluginContext) GetMap(prop string) map[string]interface{} {
	if val, ok := c.Properties[prop]; ok {
		if mp, ok := val.(map[string]interface{}); ok {
			return mp
		}
	}
	return nil
}

// ModuleHandler is the interface that custom Soong plugins must implement.
type ModuleHandler interface {
	SupportedTypes() []string
	HandleModule(ctx *PluginContext) ([]string, error)
}

// Registry manages loaded plugins and module handlers.
type Registry struct {
	handlers map[string]ModuleHandler
}

// GlobalRegistry is the default plugin registry.
var GlobalRegistry = NewRegistry()

// NewRegistry initializes an empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		handlers: make(map[string]ModuleHandler),
	}
}

// Register registers a handler for its supported module types.
func (r *Registry) Register(h ModuleHandler) {
	for _, t := range h.SupportedTypes() {
		r.handlers[t] = h
	}
}

// Get looks up a handler for a given module type.
func (r *Registry) Get(moduleType string) (ModuleHandler, bool) {
	h, ok := r.handlers[moduleType]
	return h, ok
}

// LoadPlugin dynamically loads a Go shared library plugin (.so) at runtime.
func (r *Registry) LoadPlugin(soPath string) error {
	p, err := plugin.Open(soPath)
	if err != nil {
		return fmt.Errorf("failed to open plugin %s: %w", soPath, err)
	}

	// Look for optional exported initializer: func Register(r *Registry)
	if regSym, err := p.Lookup("Register"); err == nil {
		if regFunc, ok := regSym.(func(*Registry)); ok {
			regFunc(r)
			return nil
		}
	}

	// Look for optional exported handler: var Handler ModuleHandler
	if hSym, err := p.Lookup("Handler"); err == nil {
		if handler, ok := hSym.(ModuleHandler); ok {
			r.Register(handler)
			return nil
		}
		// Also support pointer to ModuleHandler
		if handlerPtr, ok := hSym.(*ModuleHandler); ok && handlerPtr != nil {
			r.Register(*handlerPtr)
			return nil
		}
	}

	return nil
}
