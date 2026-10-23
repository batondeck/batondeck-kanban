// Package contract holds the MCP tool contract this server owes conductor's surface, and the
// comparator that measures it: canonical(conductor) ⊖ deltas.json must equal canonical(go) exactly.
//
// Canonicalize reduces a JSON Schema to the parts a caller can observe: the set of JSON types, enum
// values, items, properties, required (sorted) and additionalProperties. Everything else is
// stripped ($schema, description, default, title, the min/max family, pattern, format,
// propertyNames, propertyOrder), and an unknown keyword is an error, so the reduction never
// silently drops something that matters. The rules, one per shape the two generators emit:
//
//   - `type: T` and `type: [T, "null"]` give the typeSet {T} and {T, null}.
//   - anyOf branches that carry only a type merge into one typeSet: Zod's `anyOf: [T, null]` and Go's
//     `type: ["null", T]` reduce alike.
//   - anyOf with exactly one structured branch (properties, items, enum, additionalProperties) is
//     that branch with the other branches' types added to its typeSet (Zod's nullable object).
//   - anyOf with several structured branches keeps them as a sorted anyOf list.
//   - `const: v` is `enum: [v]`.
//   - The empty schema, `true`, and the recursive JSON-value union (string, number, boolean, null,
//     array and object, whose items and additionalProperties are absent or a $ref) give typeSet {any}.
//   - `$ref` resolves against definitions/$defs; `allOf` with one branch is that branch.
//   - additionalProperties `false` is kept, a schema is reduced, `true` and absent are both absent.
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Node is a JSON Schema reduced to what the contract compares.
type Node struct {
	TypeSet              []string         `json:"typeSet,omitempty"`
	Enum                 []any            `json:"enum,omitempty"`
	Items                *Node            `json:"items,omitempty"`
	Properties           map[string]*Node `json:"properties,omitempty"`
	Required             []string         `json:"required,omitempty"`
	AdditionalProperties *Additional      `json:"additionalProperties,omitempty"`
	AnyOf                []*Node          `json:"anyOf,omitempty"`
}

// Additional is additionalProperties: false, or a schema.
type Additional struct {
	False  bool
	Schema *Node
}

func (a Additional) MarshalJSON() ([]byte, error) {
	if a.False {
		return []byte("false"), nil
	}
	return json.Marshal(a.Schema)
}

func (a *Additional) UnmarshalJSON(b []byte) error {
	if string(bytes.TrimSpace(b)) == "false" {
		a.False = true
		return nil
	}
	return json.Unmarshal(b, &a.Schema)
}

// Tool is one tool's reduced input and output schemas.
type Tool struct {
	Input  *Node `json:"input"`
	Output *Node `json:"output"`
}

// Surface is a set of tools keyed by name, so a pointer into it reads
// /tools/<name>/<input|output>/properties/<p>/…
type Surface struct {
	Tools map[string]*Tool `json:"tools"`
}

// stripped are the keywords Canonicalize drops: they constrain or describe a value without changing
// what shape a caller sends or receives.
var stripped = map[string]bool{
	"$schema": true, "description": true, "default": true, "title": true, "examples": true,
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true,
	"minLength": true, "maxLength": true, "minItems": true, "maxItems": true,
	"pattern": true, "format": true, "propertyNames": true, "propertyOrder": true,
	"definitions": true, "$defs": true,
}

var structural = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true, "items": true,
	"enum": true, "const": true, "anyOf": true, "allOf": true, "$ref": true,
}

var jsonTypes = []string{"array", "boolean", "null", "number", "object", "string"}

// Canonicalize reduces a decoded JSON Schema (encoding/json's map[string]any form).
func Canonicalize(schema any) (*Node, error) {
	root, _ := schema.(map[string]any)
	c := canon{defs: map[string]any{}, active: map[string]bool{}}
	for _, k := range []string{"definitions", "$defs"} {
		if d, ok := root[k].(map[string]any); ok {
			for name, s := range d {
				c.defs["#/"+k+"/"+name] = s
			}
		}
	}
	return c.node(schema, "")
}

type canon struct {
	defs   map[string]any
	active map[string]bool
}

func anyNode() *Node { return &Node{TypeSet: []string{"any"}} }

func (c canon) node(schema any, at string) (*Node, error) {
	if b, ok := schema.(bool); ok {
		if b {
			return anyNode(), nil
		}
		return nil, fmt.Errorf("%s: the false schema is not supported", at)
	}
	m, ok := schema.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: a schema must be an object or a boolean, got %T", at, schema)
	}
	present := 0
	for k := range m {
		switch {
		case structural[k]:
			present++
		case stripped[k]:
		default:
			return nil, fmt.Errorf("%s: unknown keyword %q", at, k)
		}
	}
	if present == 0 {
		return anyNode(), nil
	}
	if ref, ok := m["$ref"].(string); ok {
		if present > 1 {
			return nil, fmt.Errorf("%s: $ref beside other keywords is not supported", at)
		}
		def, ok := c.defs[ref]
		if !ok {
			return nil, fmt.Errorf("%s: unresolved $ref %q", at, ref)
		}
		if isJSONValue(def) {
			return anyNode(), nil
		}
		if c.active[ref] {
			return nil, fmt.Errorf("%s: recursive $ref %q to a schema other than the JSON-value union", at, ref)
		}
		c.active[ref] = true
		defer delete(c.active, ref)
		return c.node(def, at)
	}
	if all, ok := m["allOf"].([]any); ok {
		if present > 1 || len(all) != 1 {
			return nil, fmt.Errorf("%s: allOf is supported only as the single keyword with one branch", at)
		}
		return c.node(all[0], at+"/allOf/0")
	}
	if branches, ok := m["anyOf"].([]any); ok {
		if present > 1 {
			return nil, fmt.Errorf("%s: anyOf beside other structural keywords is not supported", at)
		}
		if isJSONValue(m) {
			return anyNode(), nil
		}
		return c.anyOf(branches, at)
	}

	n := &Node{}
	switch t := m["type"].(type) {
	case nil:
	case string:
		n.TypeSet = []string{t}
	case []any:
		for _, v := range t {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("%s: type array holds %T", at, v)
			}
			n.TypeSet = append(n.TypeSet, s)
		}
	default:
		return nil, fmt.Errorf("%s: type is %T", at, t)
	}
	n.TypeSet = sortedSet(n.TypeSet)

	if e, ok := m["enum"]; ok {
		vals, ok := e.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: enum is %T", at, e)
		}
		n.Enum = vals
	}
	if v, ok := m["const"]; ok {
		if n.Enum != nil {
			return nil, fmt.Errorf("%s: enum and const together", at)
		}
		n.Enum = []any{v}
	}
	n.Enum = sortedValues(n.Enum)

	if p, ok := m["properties"]; ok {
		props, ok := p.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: properties is %T", at, p)
		}
		n.Properties = map[string]*Node{}
		for name, s := range props {
			child, err := c.node(s, at+"/properties/"+escape(name))
			if err != nil {
				return nil, err
			}
			n.Properties[name] = child
		}
	}
	if r, ok := m["required"]; ok {
		req, ok := r.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: required is %T", at, r)
		}
		for _, v := range req {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("%s: required holds %T", at, v)
			}
			n.Required = append(n.Required, s)
		}
		n.Required = sortedSet(n.Required)
	}
	if ap, ok := m["additionalProperties"]; ok {
		switch v := ap.(type) {
		case bool:
			if !v {
				n.AdditionalProperties = &Additional{False: true}
			}
		default:
			child, err := c.node(v, at+"/additionalProperties")
			if err != nil {
				return nil, err
			}
			n.AdditionalProperties = &Additional{Schema: child}
		}
	}
	if it, ok := m["items"]; ok {
		if _, tuple := it.([]any); tuple {
			return nil, fmt.Errorf("%s: tuple items are not supported", at)
		}
		child, err := c.node(it, at+"/items")
		if err != nil {
			return nil, err
		}
		n.Items = child
	}
	return n, nil
}

func (c canon) anyOf(branches []any, at string) (*Node, error) {
	var types []string
	var structured []*Node
	for i, b := range branches {
		n, err := c.node(b, fmt.Sprintf("%s/anyOf/%d", at, i))
		if err != nil {
			return nil, err
		}
		if slices.Contains(n.TypeSet, "any") {
			return anyNode(), nil
		}
		if len(n.AnyOf) > 0 {
			types = append(types, n.TypeSet...)
			structured = append(structured, n.AnyOf...)
			continue
		}
		if n.isTypeOnly() {
			types = append(types, n.TypeSet...)
			continue
		}
		structured = append(structured, n)
	}
	switch len(structured) {
	case 0:
		return &Node{TypeSet: sortedSet(types)}, nil
	case 1:
		s := structured[0]
		s.TypeSet = sortedSet(append(s.TypeSet, types...))
		return s, nil
	}
	sort.Slice(structured, func(i, j int) bool { return mustJSON(structured[i]) < mustJSON(structured[j]) })
	return &Node{TypeSet: sortedSet(types), AnyOf: structured}, nil
}

func (n *Node) isTypeOnly() bool {
	return n.Enum == nil && n.Items == nil && n.Properties == nil && n.Required == nil &&
		n.AdditionalProperties == nil && n.AnyOf == nil
}

// isJSONValue reports the recursive "any JSON value" union: anyOf branches whose types cover all
// six JSON types, with any array items and object additionalProperties absent or a $ref.
func isJSONValue(schema any) bool {
	m, ok := schema.(map[string]any)
	if !ok {
		return false
	}
	branches, ok := m["anyOf"].([]any)
	if !ok {
		return false
	}
	seen := map[string]bool{}
	for _, b := range branches {
		bm, ok := b.(map[string]any)
		if !ok {
			return false
		}
		t, ok := bm["type"].(string)
		if !ok {
			return false
		}
		for _, k := range []string{"items", "additionalProperties"} {
			if v, has := bm[k]; has {
				vm, ok := v.(map[string]any)
				if _, ref := vm["$ref"]; !ok || !ref || len(vm) != 1 {
					return false
				}
			}
		}
		for k := range bm {
			if k != "type" && k != "items" && k != "additionalProperties" && !stripped[k] {
				return false
			}
		}
		seen[t] = true
	}
	for _, t := range jsonTypes {
		if !seen[t] {
			return false
		}
	}
	return true
}

func sortedSet(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := slices.Clone(s)
	slices.Sort(out)
	return slices.Compact(out)
}

func sortedValues(v []any) []any {
	if len(v) == 0 {
		return v
	}
	out := slices.Clone(v)
	sort.Slice(out, func(i, j int) bool { return mustJSON(out[i]) < mustJSON(out[j]) })
	return out
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// enumSegment is how a pointer names an enum value: the string itself, else its JSON encoding.
func enumSegment(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return mustJSON(v)
}

func escape(s string) string { return strings.NewReplacer("~", "~0", "/", "~1").Replace(s) }

func unescape(s string) string { return strings.NewReplacer("~1", "/", "~0", "~").Replace(s) }

// Marshal is the committed byte form of a Surface: two-space indent, map keys sorted, no HTML
// escaping, one trailing newline.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Deltas are every departure from conductor's surface, each one a reviewable line.
type Deltas struct {
	RemovedTools      []string      `json:"removedTools"`
	RemovedProps      []string      `json:"removedProps"`
	RemovedEnumValues []string      `json:"removedEnumValues"`
	RenamedEnumValues []RenamedEnum `json:"renamedEnumValues"`
	AddedTools        []RawTool     `json:"addedTools"`
	AddedProps        []AddedProp   `json:"addedProps"`
}

// RenamedEnum renames the enum value at Pointer (…/enum/<value>) to To, keeping the enum's place
// in the schema: ADR-0005's TODO for conductor's claimable state.
type RenamedEnum struct {
	Pointer string `json:"pointer"`
	To      string `json:"to"`
}

// RawTool is a tool as JSON Schema, the shape of tools.json's entries and of deltas' additions.
type RawTool struct {
	Name         string `json:"name"`
	InputSchema  any    `json:"inputSchema"`
	OutputSchema any    `json:"outputSchema"`
}

// AddedProp adds a property at Pointer (…/properties/<name>), required or not.
type AddedProp struct {
	Pointer  string `json:"pointer"`
	Required bool   `json:"required"`
	Schema   any    `json:"schema"`
}

// FromRaw canonicalizes tools into a Surface; a name twice is an error.
func FromRaw(tools []RawTool) (Surface, error) {
	s := Surface{Tools: map[string]*Tool{}}
	for _, t := range tools {
		if err := s.add(t); err != nil {
			return Surface{}, err
		}
	}
	return s, nil
}

func (s Surface) add(t RawTool) error {
	if _, dup := s.Tools[t.Name]; dup {
		return fmt.Errorf("tool %s appears twice", t.Name)
	}
	in, err := Canonicalize(t.InputSchema)
	if err != nil {
		return fmt.Errorf("/tools/%s/input%w", t.Name, err)
	}
	out, err := Canonicalize(t.OutputSchema)
	if err != nil {
		return fmt.Errorf("/tools/%s/output%w", t.Name, err)
	}
	s.Tools[t.Name] = &Tool{Input: in, Output: out}
	return nil
}

// Apply subtracts d from s in place: removed tools, removed properties, removed enum values, renamed
// enum values, then added tools and added properties. Every entry must hit something: a removal of what is not there,
// or an addition of what already is, is an error, so a stale delta cannot hide in the file.
func Apply(s Surface, d Deltas) error {
	for _, name := range d.RemovedTools {
		if _, ok := s.Tools[name]; !ok {
			return fmt.Errorf("removedTools: %s is not in the surface", name)
		}
		delete(s.Tools, name)
	}
	for _, p := range d.RemovedProps {
		parent, name, err := s.propParent(p)
		if err != nil {
			return fmt.Errorf("removedProps: %w", err)
		}
		if _, ok := parent.Properties[name]; !ok {
			return fmt.Errorf("removedProps: %s is not in the surface", p)
		}
		delete(parent.Properties, name)
		parent.Required = slices.DeleteFunc(parent.Required, func(r string) bool { return r == name })
		if len(parent.Required) == 0 {
			parent.Required = nil
		}
	}
	for _, p := range d.RemovedEnumValues {
		i := strings.LastIndex(p, "/enum/")
		if i < 0 {
			return fmt.Errorf("removedEnumValues: %s does not end in /enum/<value>", p)
		}
		n, err := s.resolve(p[:i])
		if err != nil {
			return fmt.Errorf("removedEnumValues: %w", err)
		}
		value := unescape(p[i+len("/enum/"):])
		at := slices.IndexFunc(n.Enum, func(v any) bool { return enumSegment(v) == value })
		if at < 0 {
			return fmt.Errorf("removedEnumValues: %s is not in the surface", p)
		}
		n.Enum = slices.Delete(n.Enum, at, at+1)
		if len(n.Enum) == 0 {
			return fmt.Errorf("removedEnumValues: %s empties the enum", p)
		}
	}
	for _, r := range d.RenamedEnumValues {
		i := strings.LastIndex(r.Pointer, "/enum/")
		if i < 0 {
			return fmt.Errorf("renamedEnumValues: %s does not end in /enum/<value>", r.Pointer)
		}
		n, err := s.resolve(r.Pointer[:i])
		if err != nil {
			return fmt.Errorf("renamedEnumValues: %w", err)
		}
		value := unescape(r.Pointer[i+len("/enum/"):])
		at := slices.IndexFunc(n.Enum, func(v any) bool { return enumSegment(v) == value })
		if at < 0 {
			return fmt.Errorf("renamedEnumValues: %s is not in the surface", r.Pointer)
		}
		if slices.ContainsFunc(n.Enum, func(v any) bool { return enumSegment(v) == r.To }) {
			return fmt.Errorf("renamedEnumValues: %s is already in the enum at %s", r.To, r.Pointer[:i])
		}
		n.Enum[at] = r.To
		n.Enum = sortedValues(n.Enum)
	}
	for _, t := range d.AddedTools {
		if err := s.add(t); err != nil {
			return fmt.Errorf("addedTools: %w", err)
		}
	}
	for _, a := range d.AddedProps {
		parent, name, err := s.propParent(a.Pointer)
		if err != nil {
			return fmt.Errorf("addedProps: %w", err)
		}
		if _, ok := parent.Properties[name]; ok {
			return fmt.Errorf("addedProps: %s is already in the surface", a.Pointer)
		}
		n, err := Canonicalize(a.Schema)
		if err != nil {
			return fmt.Errorf("addedProps: %s%w", a.Pointer, err)
		}
		if parent.Properties == nil {
			parent.Properties = map[string]*Node{}
		}
		parent.Properties[name] = n
		if a.Required {
			parent.Required = sortedSet(append(parent.Required, name))
		}
	}
	return nil
}

// propParent splits …/properties/<name> into the node holding the property and the name.
func (s Surface) propParent(p string) (*Node, string, error) {
	i := strings.LastIndex(p, "/properties/")
	if i < 0 {
		return nil, "", fmt.Errorf("%s does not end in /properties/<name>", p)
	}
	name := p[i+len("/properties/"):]
	if strings.Contains(name, "/") {
		return nil, "", fmt.Errorf("%s does not end in /properties/<name>", p)
	}
	parent, err := s.resolve(p[:i])
	if err != nil {
		return nil, "", err
	}
	return parent, unescape(name), nil
}

// resolve walks /tools/<name>/<input|output> then properties/<p>, items, additionalProperties and
// anyOf/<i> segments to a node.
func (s Surface) resolve(p string) (*Node, error) {
	seg := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(seg) < 3 || seg[0] != "tools" {
		return nil, fmt.Errorf("%s does not start /tools/<name>/<input|output>", p)
	}
	t, ok := s.Tools[unescape(seg[1])]
	if !ok {
		return nil, fmt.Errorf("%s: no tool %s", p, seg[1])
	}
	var n *Node
	switch seg[2] {
	case "input":
		n = t.Input
	case "output":
		n = t.Output
	default:
		return nil, fmt.Errorf("%s: %q is neither input nor output", p, seg[2])
	}
	for i := 3; i < len(seg); i++ {
		switch seg[i] {
		case "properties":
			if i+1 >= len(seg) {
				return nil, fmt.Errorf("%s: properties without a name", p)
			}
			i++
			n = n.Properties[unescape(seg[i])]
		case "items":
			n = n.Items
		case "additionalProperties":
			if n.AdditionalProperties == nil {
				n = nil
			} else {
				n = n.AdditionalProperties.Schema
			}
		case "anyOf":
			if i+1 >= len(seg) {
				return nil, fmt.Errorf("%s: anyOf without an index", p)
			}
			i++
			var k int
			if _, err := fmt.Sscanf(seg[i], "%d", &k); err != nil || k < 0 || k >= len(n.AnyOf) {
				return nil, fmt.Errorf("%s: no anyOf branch %s", p, seg[i])
			}
			n = n.AnyOf[k]
		default:
			return nil, fmt.Errorf("%s: unknown segment %q", p, seg[i])
		}
		if n == nil {
			return nil, fmt.Errorf("%s is not in the surface", strings.Join(seg[:i+1], "/"))
		}
	}
	return n, nil
}

// Walk calls fn for every node of s with its pointer, tools in name order.
func (s Surface) Walk(fn func(pointer string, n *Node)) {
	names := make([]string, 0, len(s.Tools))
	for name := range s.Tools {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		t := s.Tools[name]
		walk("/tools/"+escape(name)+"/input", t.Input, fn)
		walk("/tools/"+escape(name)+"/output", t.Output, fn)
	}
}

func walk(p string, n *Node, fn func(string, *Node)) {
	if n == nil {
		return
	}
	fn(p, n)
	props := make([]string, 0, len(n.Properties))
	for k := range n.Properties {
		props = append(props, k)
	}
	slices.Sort(props)
	for _, k := range props {
		walk(p+"/properties/"+escape(k), n.Properties[k], fn)
	}
	walk(p+"/items", n.Items, fn)
	if n.AdditionalProperties != nil {
		walk(p+"/additionalProperties", n.AdditionalProperties.Schema, fn)
	}
	for i, b := range n.AnyOf {
		walk(fmt.Sprintf("%s/anyOf/%d", p, i), b, fn)
	}
}

// Diff lists the pointers at which want and got differ, in walk order: a node present on one side
// only, or a node whose own fields (typeSet, enum, required, additionalProperties false) differ.
func Diff(want, got Surface) []string {
	flat := func(s Surface) map[string]string {
		m := map[string]string{}
		s.Walk(func(p string, n *Node) {
			own := *n
			own.Properties, own.Items, own.AnyOf = nil, nil, nil
			if own.AdditionalProperties != nil && !own.AdditionalProperties.False {
				own.AdditionalProperties = &Additional{Schema: &Node{}}
			}
			m[p] = mustJSON(own)
		})
		return m
	}
	w, g := flat(want), flat(got)
	var out []string
	for p, v := range w {
		switch gv, ok := g[p]; {
		case !ok:
			out = append(out, "missing "+p)
		case gv != v:
			out = append(out, fmt.Sprintf("differs %s: want %s, got %s", p, v, gv))
		}
	}
	for p := range g {
		if _, ok := w[p]; !ok {
			out = append(out, "extra "+p)
		}
	}
	slices.Sort(out)
	return out
}

// Forbidden are tools no surface of this server may carry: the transitions editor and requeue_task
// are removed by ruling, not merely not yet ported.
var Forbidden = []string{"get_transitions", "set_transitions", "requeue_task"}

// Added are the tools conductor lacks that every surface of this server must carry: delete_task
// (plan §Fold 2) and cancel_task (ADR-0001).
var Added = []string{"cancel_task", "delete_task"}

// CheckSurface refuses a surface that carries a forbidden tool or lacks an added one.
func CheckSurface(s Surface) error {
	for _, name := range Forbidden {
		if _, ok := s.Tools[name]; ok {
			return fmt.Errorf("surface carries %s, which is removed", name)
		}
	}
	for _, name := range Added {
		if _, ok := s.Tools[name]; !ok {
			return fmt.Errorf("surface lacks %s", name)
		}
	}
	return nil
}

// SourceFile is the shape of tools.json and admin-tools.json.
type SourceFile struct {
	Source map[string]string `json:"source"`
	Tools  []RawTool         `json:"tools"`
}

// ReadJSON decodes the file at path into v.
func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Derive computes canonical(tools.json + admin-tools.json) ⊖ deltas.json from the files in dir: the
// surface expected.json must equal.
func Derive(dir string) (Surface, error) {
	var tools, admin SourceFile
	var d Deltas
	for _, f := range []struct {
		name string
		v    any
	}{{"tools.json", &tools}, {"admin-tools.json", &admin}, {"deltas.json", &d}} {
		if err := ReadJSON(filepath.Join(dir, f.name), f.v); err != nil {
			return Surface{}, err
		}
	}
	s, err := FromRaw(append(slices.Clone(tools.Tools), admin.Tools...))
	if err != nil {
		return Surface{}, err
	}
	if err := Apply(s, d); err != nil {
		return Surface{}, err
	}
	return s, nil
}
