package registry_test

import (
	"strings"
	"testing"

	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

func TestSuggest(t *testing.T) {
	cases := []struct {
		name string
		want string
		ok   bool
	}{{"raduis", "radius", true}, {"hieght", "height", true}, {"segmnts", "segments", true}, {"zzzzzz", "", false}, {"q", "", false}}
	for _, c := range cases {
		got, ok := registry.Suggest(c.name, []string{"radius", "height", "segments", "axis"})
		if got != c.want || ok != c.ok {
			t.Errorf("Suggest(%q) = %q, %v", c.name, got, ok)
		}
	}
}

func TestTableIsConsistent(t *testing.T) {
	for _, k := range registry.Kinds() {
		seen := map[string]bool{}
		for _, f := range k.Fields {
			if seen[f.Name] {
				t.Errorf("%s repeats field %s", k.Name, f.Name)
			}
			seen[f.Name] = true
			if f.Required && f.Default != nil {
				t.Errorf("%s.%s is required and has a default", k.Name, f.Name)
			}
			if f.Default != nil {
				if dim, ok := f.Type.Dim(); ok && f.Default.Kind == units.KNumber && f.Default.Dim != dim {
					t.Errorf("%s.%s default has dimension %s, type wants %s", k.Name, f.Name, f.Default.Dim, dim)
				}
			}
			if f.Type.Kind == registry.TEnum && f.Default != nil {
				ok := false
				for _, e := range f.Type.Enum {
					ok = ok || e == f.Default.Str
				}
				if !ok {
					t.Errorf("%s.%s default %q is not a member", k.Name, f.Name, f.Default.Str)
				}
			}
		}
		for _, r := range k.Rows {
			if r.MinRows < 1 || len(r.Columns) == 0 {
				t.Errorf("%s row %s is malformed", k.Name, r.Name)
			}
		}
		for _, b := range k.Blocks {
			_, inShape := registry.Lookup(registry.CtxShape, b)
			_, inDoc := registry.Lookup(registry.CtxDocument, b)
			_, inAtlas := registry.Lookup(registry.CtxAtlas, b)
			_, inRig := registry.Lookup(registry.CtxRig, b)
			if !inShape && !inDoc && !inAtlas && !inRig {
				t.Errorf("%s lists unknown block %s", k.Name, b)
			}
		}
	}
}

func TestSubsetKinds(t *testing.T) {
	for _, name := range []string{"cylinder", "sphere", "sweep", "lathe", "array", "instance", "mirror", "socket"} {
		if _, ok := registry.Lookup(registry.CtxPart, name); !ok {
			t.Errorf("part kind %s is missing", name)
		}
	}
	for _, name := range []string{"capsule", "box", "sphere", "mirror"} {
		if _, ok := registry.Lookup(registry.CtxCollision, name); !ok {
			t.Errorf("collision kind %s is missing", name)
		}
	}
	cyl, _ := registry.Lookup(registry.CtxPart, "cylinder")
	if f, ok := cyl.Field("radius"); !ok || !f.Required || !f.Positive {
		t.Errorf("radius: %+v", f)
	}
	if f, _ := cyl.Field("segments"); f.Default == nil || f.Default.Num != 16 {
		t.Errorf("segments: %+v", f)
	}
	if !registry.IsKnownUnsupported(registry.CtxPart, "torus") || registry.IsKnownUnsupported(registry.CtxPart, "cylinder") {
		t.Error("unsupported list is wrong")
	}
}

func TestReferenceIsDeterministic(t *testing.T) {
	a, b := registry.Reference(), registry.Reference()
	if a != b || !strings.Contains(a, "## cylinder") || !strings.Contains(a, "| radius |") {
		t.Error("reference is not stable or lacks the cylinder")
	}
}
