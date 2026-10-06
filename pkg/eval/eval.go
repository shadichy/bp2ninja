package eval

import (
	"fmt"
	"path/filepath"
	"strings"

	"bp2ninja/pkg/parser"
)

// EvaluatedModule represents an Android.bp module with resolved properties and defaults.
type EvaluatedModule struct {
	Type       string
	Name       string
	Dir        string // Relative directory of this module from root (e.g. "tests", "tools", or "")
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
	IsHost          bool
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
		IsHost:          false,
	}
}

// EvalFile processes an AST file at the root level.
func (c *Context) EvalFile(file *parser.File) error {
	return c.EvalFileInDir(file, "")
}

// EvalFileInDir processes an AST file located in subDir relative to the root Ninja file.
func (c *Context) EvalFileInDir(file *parser.File, subDir string) error {
	// First pass: collect variable assignments and defaults
	var deferredModules []*parser.Module

	for _, def := range file.Defs {
		switch d := def.(type) {
		case *parser.Assignment:
			val := c.evalExpression(d.Value)
			c.Scope.vars[d.Name] = val

		case *parser.Module:
			mod := c.evalModule(d, subDir)
			if strings.HasSuffix(mod.Type, "_defaults") || mod.Type == "defaults" {
				c.Defaults[mod.Name] = mod
			} else {
				deferredModules = append(deferredModules, d)
			}
		}
	}

	// Second pass: process normal modules and apply defaults, config vars, arch, multilib, and target
	for _, rawMod := range deferredModules {
		mod := c.evalModule(rawMod, subDir)
		c.applyDefaults(mod)
		c.applySoongConfigVariables(mod)
		c.applyArch(mod)
		c.applyMultilib(mod)
		c.applyTarget(mod)
		c.applyVariantBlock(mod)
		if c.IsHost && !isDeclarativeMetaModule(mod.Type) && !mod.IsHostSupported() {
			mod.Properties["enabled"] = false
		}
		c.Modules = append(c.Modules, mod)
	}

	return nil
}

// ExtractSubdirs inspects an AST for subdirs = [...], optional_subdirs = [...], build = [...]
func ExtractSubdirs(file *parser.File) (subdirs []string, buildFiles []string) {
	for _, def := range file.Defs {
		if a, ok := def.(*parser.Assignment); ok {
			switch a.Name {
			case "subdirs", "optional_subdirs":
				if list, ok := a.Value.(*parser.List); ok {
					for _, val := range list.Values {
						if str, ok := val.(*parser.String); ok {
							subdirs = append(subdirs, str.Value)
						}
					}
				}
			case "build":
				if list, ok := a.Value.(*parser.List); ok {
					for _, val := range list.Values {
						if str, ok := val.(*parser.String); ok {
							buildFiles = append(buildFiles, str.Value)
						}
					}
				}
			}
		}
	}
	return subdirs, buildFiles
}

func (c *Context) evalModule(m *parser.Module, subDir string) *EvaluatedModule {
	mod := &EvaluatedModule{
		Type:       m.Type,
		Dir:        subDir,
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
func cloneProperty(val interface{}) interface{} {
	switch v := val.(type) {
	case []interface{}:
		res := make([]interface{}, len(v))
		for i, x := range v {
			res[i] = cloneProperty(x)
		}
		return res
	case []string:
		res := make([]string, len(v))
		copy(res, v)
		return res
	case map[string]interface{}:
		res := make(map[string]interface{}, len(v))
		for k, item := range v {
			res[k] = cloneProperty(item)
		}
		return res
	default:
		return val
	}
}

func (c *Context) applyDefaults(mod *EvaluatedModule) {
	defaultsList := mod.GetStringList("defaults")
	if len(defaultsList) == 0 {
		return
	}
	delete(mod.Properties, "defaults")

	// Accumulated properties from all defaults in order
	mergedDefaults := make(map[string]interface{})

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
			valToMerge := cloneProperty(defVal)
			if defMod.Dir != mod.Dir && (k == "srcs" || k == "local_include_dirs" || k == "export_include_dirs" || k == "arch" || k == "target" || k == "multilib") {
				valToMerge = adjustPathProperty(valToMerge, defMod.Dir, mod.Dir)
			}
			if existing, exists := mergedDefaults[k]; exists {
				mergedDefaults[k] = mergeProperties(existing, valToMerge)
			} else {
				mergedDefaults[k] = valToMerge
			}
		}
	}

	// Now merge mod's own properties on top of mergedDefaults
	for k, defVal := range mergedDefaults {
		if modVal, exists := mod.Properties[k]; exists {
			mod.Properties[k] = mergeProperties(defVal, modVal)
		} else {
			mod.Properties[k] = defVal
		}
	}
}

func adjustPathProperty(val interface{}, fromDir, toDir string) interface{} {
	switch v := val.(type) {
	case string:
		if !filepath.IsAbs(v) && !strings.HasPrefix(v, ":") {
			full := filepath.Join(fromDir, v)
			if rel, err := filepath.Rel(toDir, full); err == nil {
				return rel
			}
		}
		return v
	case []interface{}:
		var res []interface{}
		for _, item := range v {
			res = append(res, adjustPathProperty(item, fromDir, toDir))
		}
		return res
	case []string:
		var res []interface{}
		for _, item := range v {
			if !filepath.IsAbs(item) && !strings.HasPrefix(item, ":") {
				full := filepath.Join(fromDir, item)
				if rel, err := filepath.Rel(toDir, full); err == nil {
					res = append(res, rel)
					continue
				}
			}
			res = append(res, item)
		}
		return res
	case map[string]interface{}:
		res := make(map[string]interface{}, len(v))
		for k, item := range v {
			res[k] = adjustPathProperty(item, fromDir, toDir)
		}
		return res
	default:
		return val
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
	targetKey := "android"
	if c.IsHost || strings.HasSuffix(mod.Type, "_host") || (!mod.IsDeviceSupported() && mod.IsHostSupported()) {
		targetKey = "host"
	}

	// Expand top-level host or android block if present
	if targetKey == "host" {
		if hostProps, ok := mod.Properties["host"].(map[string]interface{}); ok {
			for propName, propVal := range hostProps {
				if existing, exists := mod.Properties[propName]; exists {
					mod.Properties[propName] = mergeProperties(existing, propVal)
				} else {
					mod.Properties[propName] = propVal
				}
			}
		}
	} else {
		if androidProps, ok := mod.Properties["android"].(map[string]interface{}); ok {
			for propName, propVal := range androidProps {
				if existing, exists := mod.Properties[propName]; exists {
					mod.Properties[propName] = mergeProperties(existing, propVal)
				} else {
					mod.Properties[propName] = propVal
				}
			}
		}
	}

	rawTarget, ok := mod.Properties["target"].(map[string]interface{})
	if !ok {
		return
	}

	// Apply targets cumulatively
	var targetKeys []string
	if targetKey == "host" {
		targetKeys = []string{"host", "not_windows", "linux", "linux_glibc", "glibc"}
		if c.TargetArch != "" {
			targetKeys = append(targetKeys, "linux_"+c.TargetArch, "linux_glibc_"+c.TargetArch)
		}
	} else {
		targetKeys = []string{"android", "bionic"}
		if c.TargetArch != "" {
			targetKeys = append(targetKeys, "android_"+c.TargetArch)
		}
	}

	for _, key := range targetKeys {
		if targetProps, found := rawTarget[key].(map[string]interface{}); found {
			for propName, propVal := range targetProps {
				if existing, exists := mod.Properties[propName]; exists {
					mod.Properties[propName] = mergeProperties(existing, propVal)
				} else {
					mod.Properties[propName] = propVal
				}
			}
		}
	}
}

// applyVariantBlock resolves static: {} or shared: {} sub-blocks for single-variant modules.
func (c *Context) applyVariantBlock(mod *EvaluatedModule) {
	switch mod.Type {
	case "cc_library_static", "cc_library_host_static":
		mod.ApplyVariant("static")
	case "cc_library_shared", "cc_library_host_shared":
		mod.ApplyVariant("shared")
	case "cc_binary", "cc_binary_host", "cc_test", "cc_test_host", "cc_benchmark", "cc_fuzz":
		if mod.GetBool("static_executable") {
			mod.ApplyVariant("static")
		} else {
			mod.ApplyVariant("shared")
		}
	}
}

func toInterfaceSlice(v interface{}) ([]interface{}, bool) {
	switch s := v.(type) {
	case []interface{}:
		return s, true
	case []string:
		res := make([]interface{}, len(s))
		for i, x := range s {
			res[i] = x
		}
		return res, true
	default:
		return nil, false
	}
}

func mergeProperties(base, override interface{}) interface{} {
	if bSlice, okB := toInterfaceSlice(base); okB {
		if oSlice, okO := toInterfaceSlice(override); okO {
			res := make([]interface{}, len(bSlice)+len(oSlice))
			copy(res, bSlice)
			copy(res[len(bSlice):], oSlice)
			return res
		}
	}
	switch b := base.(type) {
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

func (m *EvaluatedModule) IsHostSupported() bool {
	if m == nil {
		return false
	}
	if strings.HasSuffix(m.Type, "_host") {
		return true
	}
	if m.GetBool("host_supported") || m.GetBool("host_available") {
		return true
	}
	return false
}

func (m *EvaluatedModule) IsDeviceSupported() bool {
	if m == nil {
		return false
	}
	if strings.HasSuffix(m.Type, "_host") {
		return false
	}
	if val, ok := m.Properties["device_supported"]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return true
}

func isDeclarativeMetaModule(modType string) bool {
	switch modType {
	case "package", "license", "license_kind", "package_metadata",
		"soong_namespace", "soong_config_module_type", "soong_config_string_variable",
		"soong_config_bool_variable", "soong_config_module_type_import",
		"filegroup", "phony", "ndk_headers", "ndk_library", "vintf_fragment",
		"cc_defaults", "java_defaults":
		return true
	}
	return false
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
	} else if strList, ok := val.([]string); ok {
		res = append(res, strList...)
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

// ApplyVariant merges properties from the given variant ("static" or "shared")
// into m.Properties, and removes both "static" and "shared" keys.
func (m *EvaluatedModule) ApplyVariant(variant string) {
	if m == nil {
		return
	}
	if variantProps, ok := m.Properties[variant].(map[string]interface{}); ok {
		for propName, propVal := range variantProps {
			if existing, exists := m.Properties[propName]; exists {
				m.Properties[propName] = mergeProperties(existing, propVal)
			} else {
				m.Properties[propName] = propVal
			}
		}
	}
	delete(m.Properties, "static")
	delete(m.Properties, "shared")
}

// WithVariant returns a shallow copy of m with variant-specific properties merged in.
func (m *EvaluatedModule) WithVariant(variant string) *EvaluatedModule {
	if m == nil {
		return nil
	}
	clone := &EvaluatedModule{
		Type:       m.Type,
		Name:       m.Name,
		Dir:        m.Dir,
		Properties: make(map[string]interface{}, len(m.Properties)),
		Raw:        m.Raw,
	}
	for k, v := range m.Properties {
		clone.Properties[k] = v
	}
	clone.ApplyVariant(variant)
	return clone
}

// GetAllStringList retrieves a string list property, checking both module-level properties
// and any nested static/shared variant blocks (useful for include directory resolution).
func (m *EvaluatedModule) GetAllStringList(prop string) []string {
	res := m.GetStringList(prop)
	if staticMap, ok := m.Properties["static"].(map[string]interface{}); ok {
		res = append(res, toStringList(staticMap[prop])...)
	}
	if sharedMap, ok := m.Properties["shared"].(map[string]interface{}); ok {
		res = append(res, toStringList(sharedMap[prop])...)
	}
	return res
}

func toStringList(val interface{}) []string {
	var res []string
	if val == nil {
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
	} else if list, ok := val.([]string); ok {
		res = append(res, list...)
	}
	return res
}
