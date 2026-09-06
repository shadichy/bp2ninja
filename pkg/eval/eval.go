package eval

import (
	"fmt"
	"strings"

	"bp2ninja/pkg/parser"
)

// EvaluatedModule represents an Android.bp module with resolved properties and defaults.
type EvaluatedModule struct {
	Type       string
	Name       string
	Properties map[string]interface{}
	Raw        *parser.Module
}

// Scope tracks variables defined in an Android.bp file.
type Scope struct {
	vars map[string]interface{}
}

// NewScope creates a new evaluation scope.
func NewScope() *Scope {
	return &Scope{vars: make(map[string]interface{})}
}

// Context coordinates AST evaluation across multiple modules and defaults.
type Context struct {
	Scope           *Scope
	Defaults        map[string]*EvaluatedModule
	Modules         []*EvaluatedModule
	ConfigVariables map[string]string // e.g. "target_board_platform" -> "sm8450"
	TargetArch      string
}

// NewContext creates an evaluation context.
func NewContext(configVars map[string]string, targetArch string) *Context {
	if configVars == nil {
		configVars = make(map[string]string)
	}
	if targetArch == "" {
		targetArch = "arm64"
	}
	return &Context{
		Scope:           NewScope(),
		Defaults:        make(map[string]*EvaluatedModule),
		Modules:         make([]*EvaluatedModule, 0),
		ConfigVariables: configVars,
		TargetArch:      targetArch,
	}
}

// EvalFile processes an AST file, resolving top-level assignments and modules.
func (c *Context) EvalFile(file *parser.File) error {
	// First pass: collect variable assignments and defaults
	var deferredModules []*parser.Module

	for _, def := range file.Defs {
		switch d := def.(type) {
		case *parser.Assignment:
			val := c.evalExpression(d.Value)
			c.Scope.vars[d.Name] = val

		case *parser.Module:
			mod := c.evalModule(d)
			if strings.HasSuffix(mod.Type, "_defaults") || mod.Type == "defaults" {
				c.Defaults[mod.Name] = mod
			} else {
				deferredModules = append(deferredModules, d)
			}
		}
	}

	// Second pass: process normal modules and apply defaults, config vars, arch, multilib, and target
	for _, rawMod := range deferredModules {
		mod := c.evalModule(rawMod)
		c.applyDefaults(mod)
		c.applySoongConfigVariables(mod)
		c.applyArch(mod)
		c.applyMultilib(mod)
		c.applyTarget(mod)
		c.Modules = append(c.Modules, mod)
	}

	return nil
}

func (c *Context) evalModule(m *parser.Module) *EvaluatedModule {
	mod := &EvaluatedModule{
		Type:       m.Type,
		Properties: make(map[string]interface{}),
		Raw:        m,
	}

	for _, prop := range m.Properties {
		val := c.evalExpression(prop.Value)
		mod.Properties[prop.Name] = val
		if prop.Name == "name" {
			if s, ok := val.(string); ok {
				mod.Name = s
			}
		}
	}

	return mod
}

func (c *Context) evalExpression(expr parser.Expression) interface{} {
	switch e := expr.(type) {
	case *parser.String:
		return e.Value
	case *parser.Int64:
		return e.Value
	case *parser.Bool:
		return e.Value
	case *parser.List:
		var list []interface{}
		for _, v := range e.Values {
			evaluated := c.evalExpression(v)
			list = append(list, evaluated)
		}
		return list
	case *parser.Map:
		m := make(map[string]interface{})
		for _, prop := range e.Properties {
			m[prop.Name] = c.evalExpression(prop.Value)
		}
		return m
	case *parser.Variable:
		if val, ok := c.Scope.vars[e.Name]; ok {
			return val
		}
		return e.Name
	case *parser.Operator:
		left := c.evalExpression(e.Args[0])
		right := c.evalExpression(e.Args[1])
		return c.evalBinaryOp(e.Operator, left, right)
	default:
		return fmt.Sprintf("%v", expr)
	}
}

func (c *Context) evalBinaryOp(op rune, left, right interface{}) interface{} {
	if op == '+' {
		switch l := left.(type) {
		case string:
			if r, ok := right.(string); ok {
				return l + r
			}
		case []interface{}:
			if r, ok := right.([]interface{}); ok {
				res := make([]interface{}, len(l)+len(r))
				copy(res, l)
				copy(res[len(l):], r)
				return res
			}
		}
	}
	return left
}

// applyDefaults flattens any `defaults: ["..."]` referenced by the module.
func (c *Context) applyDefaults(mod *EvaluatedModule) {
	defaultsList := mod.GetStringList("defaults")
	if len(defaultsList) == 0 {
		return
	}
	delete(mod.Properties, "defaults")

	// For each referenced defaults module, merge its properties
	for _, defName := range defaultsList {
		defMod, ok := c.Defaults[defName]
		if !ok {
			continue
		}
		// First apply any parent defaults the defaults module itself has
		c.applyDefaults(defMod)

		for k, defVal := range defMod.Properties {
			if k == "name" || k == "defaults" {
				continue
			}
			modVal, exists := mod.Properties[k]
			if !exists {
				mod.Properties[k] = defVal
			} else {
				// Merge lists or maps
				mod.Properties[k] = mergeProperties(defVal, modVal)
			}
		}
	}
}

// applySoongConfigVariables expands conditional properties based on context config variables.
func (c *Context) applySoongConfigVariables(mod *EvaluatedModule) {
	rawConfigVars, ok := mod.Properties["soong_config_variables"].(map[string]interface{})
	if !ok {
		return
	}

	for varName, varBranches := range rawConfigVars {
		activeVal, hasVar := c.ConfigVariables[varName]
		branchesMap, isMap := varBranches.(map[string]interface{})
		if !isMap {
			continue
		}

		if hasVar {
			// Branch matches specific value
			if branchProps, matched := branchesMap[activeVal].(map[string]interface{}); matched {
				for propName, propVal := range branchProps {
					if existing, found := mod.Properties[propName]; found {
						mod.Properties[propName] = mergeProperties(existing, propVal)
					} else {
						mod.Properties[propName] = propVal
					}
				}
			}
		}
	}
}

// applyArch expands architecture-specific properties based on Context.TargetArch.
func (c *Context) applyArch(mod *EvaluatedModule) {
	if c.TargetArch == "" {
		return
	}
	rawArch, ok := mod.Properties["arch"].(map[string]interface{})
	if !ok {
		return
	}

	if archProps, found := rawArch[c.TargetArch].(map[string]interface{}); found {
		for propName, propVal := range archProps {
			if existing, exists := mod.Properties[propName]; exists {
				mod.Properties[propName] = mergeProperties(existing, propVal)
			} else {
				mod.Properties[propName] = propVal
			}
		}
	}
}

// applyMultilib expands multilib (lib32 vs lib64) properties based on target arch.
func (c *Context) applyMultilib(mod *EvaluatedModule) {
	rawMultilib, ok := mod.Properties["multilib"].(map[string]interface{})
	if !ok {
		return
	}

	key := "lib64"
	if c.TargetArch == "arm" || c.TargetArch == "x86" {
		key = "lib32"
	}

	if props, found := rawMultilib[key].(map[string]interface{}); found {
		for propName, propVal := range props {
			if existing, exists := mod.Properties[propName]; exists {
				mod.Properties[propName] = mergeProperties(existing, propVal)
			} else {
				mod.Properties[propName] = propVal
			}
		}
	}
}

// applyTarget expands OS/target-specific properties (android vs host/linux).
func (c *Context) applyTarget(mod *EvaluatedModule) {
	rawTarget, ok := mod.Properties["target"].(map[string]interface{})
	if !ok {
		return
	}

	targetKey := "android"
	if strings.HasSuffix(mod.Type, "_host") || mod.GetBool("host_supported") {
		targetKey = "host"
	}

	if targetProps, found := rawTarget[targetKey].(map[string]interface{}); found {
		for propName, propVal := range targetProps {
			if existing, exists := mod.Properties[propName]; exists {
				mod.Properties[propName] = mergeProperties(existing, propVal)
			} else {
				mod.Properties[propName] = propVal
			}
		}
	} else if targetKey == "host" {
		for _, altKey := range []string{"linux", "linux_glibc"} {
			if altProps, altFound := rawTarget[altKey].(map[string]interface{}); altFound {
				for propName, propVal := range altProps {
					if existing, exists := mod.Properties[propName]; exists {
						mod.Properties[propName] = mergeProperties(existing, propVal)
					} else {
						mod.Properties[propName] = propVal
					}
				}
				break
			}
		}
	}
}

func mergeProperties(base, override interface{}) interface{} {
	switch b := base.(type) {
	case []interface{}:
		if o, ok := override.([]interface{}); ok {
			res := make([]interface{}, len(b)+len(o))
			copy(res, b)
			copy(res[len(b):], o)
			return res
		}
	case map[string]interface{}:
		if o, ok := override.(map[string]interface{}); ok {
			res := make(map[string]interface{})
			for k, v := range b {
				res[k] = v
			}
			for k, v := range o {
				if ex, ok := res[k]; ok {
					res[k] = mergeProperties(ex, v)
				} else {
					res[k] = v
				}
			}
			return res
		}
	}
	return override
}

// Helper methods on EvaluatedModule

func (m *EvaluatedModule) GetString(prop string) string {
	if val, ok := m.Properties[prop]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}

func (m *EvaluatedModule) GetBool(prop string) bool {
	if val, ok := m.Properties[prop]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

func (m *EvaluatedModule) IsEnabled() bool {
	if val, ok := m.Properties["enabled"]; ok {
		if b, ok := val.(bool); ok && !b {
			return false
		}
	}
	return true
}

func (m *EvaluatedModule) GetStringList(prop string) []string {
	var res []string
	val, ok := m.Properties[prop]
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

func (m *EvaluatedModule) GetMap(prop string) map[string]interface{} {
	if val, ok := m.Properties[prop]; ok {
		if mp, ok := val.(map[string]interface{}); ok {
			return mp
		}
	}
	return nil
}
