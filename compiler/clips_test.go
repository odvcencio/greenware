package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/lang"
)

// The clip library of the athlete (examples/athlete/clips) is held to the
// game's own animation code: testdata/clips/vectors.json holds the keys that
// cmd/bakeathletes/pose.go of the game writes, as float64, and the compiled
// fixture's animations are decoded from the GLB and compared with them.

const (
	clipFixture = "../testdata/clips/fixture.gw"
	clipVectors = "../testdata/clips/vectors.json"
	clipDigest  = "../testdata/clips/animations.candidate.sha256"
	// The game's file is in yards; Greenware writes metres.
	yardMetres = 0.9144
	// A quaternion may differ in sign; the distance is the smaller of the two.
	clipTolerance = 1e-6
)

type clipVec struct {
	Joints     []string `json:"joints"`
	Animations []string `json:"animations"`
	Clips      []struct {
		Name     string  `json:"name"`
		Duration float64 `json:"duration"`
		Keys     int     `json:"keys"`
		Samples  []struct {
			Key  int                   `json:"key"`
			Time float64               `json:"time"`
			Root [3]float64            `json:"root"`
			Rot  map[string][4]float64 `json:"rot"`
		} `json:"samples"`
	} `json:"clips"`
	NodeClips []struct {
		Name   string      `json:"name"`
		Node   string      `json:"node"`
		Path   string      `json:"path"`
		Times  []float64   `json:"times"`
		Values [][]float64 `json:"values"`
	} `json:"nodeClips"`
}

// buildClipFixture builds the fixture in the project rooted at the repository.
func buildClipFixture(t testing.TB) ([]byte, *gltf.Document) {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(clipFixture))
	if err != nil {
		t.Fatal(err)
	}
	proj, err := ast.OpenProject(filepath.FromSlash(clipFixture), "..")
	if err != nil {
		t.Fatal(err)
	}
	f, ds := ast.LowerWith(lang.Parse(src), ast.Options{Project: proj})
	if diag.HasErrors(ds) {
		t.Fatalf("lower: %v", ds)
	}
	p, ds := Build(f, "")
	if p == nil {
		t.Fatalf("the clip fixture does not build: %v", ds)
	}
	for _, d := range ds {
		t.Errorf("diagnostic: %s", d.Message)
	}
	glb := p.Outputs[0].Files[0].GLB
	d, err := gltf.Decode(glb)
	if err != nil {
		t.Fatal(err)
	}
	return glb, d
}

func readClipVectors(t testing.TB) *clipVec {
	t.Helper()
	path := clipVectors
	if p := os.Getenv("GW_CLIP_VECTORS"); p != "" {
		path = p // a dump of every key, for checking the whole table
	}
	b, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	var v clipVec
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

// quatDistance is the distance between two rotations, ignoring the sign.
func quatDistance(a [4]float64, b [4]float32) float64 {
	var plus, minus float64
	for i := range a {
		plus += (a[i] - float64(b[i])) * (a[i] - float64(b[i]))
		minus += (a[i] + float64(b[i])) * (a[i] + float64(b[i]))
	}
	return math.Sqrt(math.Min(plus, minus))
}

func TestClipLibraryMatchesTheGameBake(t *testing.T) {
	_, d := buildClipFixture(t)
	v := readClipVectors(t)

	// The names: the same 31 animations as the game's file. Greenware writes
	// them in the order of their delivered names, so the lists are compared
	// sorted and with their multiplicity.
	var got []string
	byName := map[string]*gltf.Animation{}
	for i := range d.Animations {
		got = append(got, d.Animations[i].Name)
		byName[d.Animations[i].Name] = &d.Animations[i]
	}
	want := append([]string(nil), v.Animations...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("animations\n got %v\nwant %v", got, want)
	}
	if len(want) != 31 {
		t.Errorf("the game's file has %d animations, expected 27 body clips and 4 wrapper clips", len(want))
	}

	nodeName := func(n int) string { return d.Nodes[n].Name }
	var maxQuat, maxRoot, maxTime float64
	for _, c := range v.Clips {
		a := byName[c.Name]
		type channel struct {
			in  []float32
			out []float32
		}
		rot, trans := map[string]channel{}, map[string]channel{}
		for _, ch := range a.Channels {
			s := a.Samplers[ch.Sampler]
			switch ch.Path {
			case gltf.PathRotation:
				rot[nodeName(ch.Node)] = channel{s.Input, s.Output}
			case gltf.PathTranslation:
				trans[nodeName(ch.Node)] = channel{s.Input, s.Output}
			}
		}
		for _, s := range c.Samples {
			// Times: the sampled key times.
			any := false
			for j, ch := range rot {
				if len(ch.in) != c.Keys {
					t.Fatalf("%s: joint %s has %d keys, the game has %d", c.Name, j, len(ch.in), c.Keys)
				}
				any = true
			}
			if !any {
				t.Fatalf("%s has no rotation channel", c.Name)
			}
			for _, j := range v.Joints {
				ch, ok := rot[j]
				var q [4]float32
				if ok {
					copy(q[:], ch.out[4*s.Key:4*s.Key+4])
					maxTime = math.Max(maxTime, math.Abs(float64(ch.in[s.Key])-s.Time))
				} else {
					q = [4]float32{0, 0, 0, 1} // a joint the pose leaves out holds its bind rotation, the identity
				}
				e := quatDistance(s.Rot[j], q)
				maxQuat = math.Max(maxQuat, e)
				if e > clipTolerance {
					t.Errorf("%s key %d joint %s: rotation %v, the game's %v (distance %.3g)", c.Name, s.Key, j, q, s.Rot[j], e)
				}
			}
			tr, ok := trans["pelvis"]
			if !ok {
				t.Errorf("%s has no root translation", c.Name)
				continue
			}
			for i := 0; i < 3; i++ {
				e := math.Abs(float64(tr.out[3*s.Key+i]) - s.Root[i]*yardMetres)
				maxRoot = math.Max(maxRoot, e)
				if e > clipTolerance {
					t.Errorf("%s key %d root[%d]: %v m, the game's %v yd = %v m", c.Name, s.Key, i, tr.out[3*s.Key+i], s.Root[i], s.Root[i]*yardMetres)
				}
			}
			if math.Abs(float64(tr.in[len(tr.in)-1])-c.Duration) > clipTolerance {
				t.Errorf("%s ends at %v s, the game's clip lasts %v s", c.Name, tr.in[len(tr.in)-1], c.Duration)
			}
		}
	}
	t.Logf("max quaternion distance %.3g, max root error %.3g m, max time error %.3g s, over %d clips", maxQuat, maxRoot, maxTime, len(v.Clips))

	// The four wrapper clips: translations are field yards (metres here),
	// headings are quaternions about y, the times are field coordinates.
	for _, n := range v.NodeClips {
		a := byName[n.Name]
		if a == nil || len(a.Channels) != 1 {
			t.Fatalf("the wrapper clip %s is missing or has more than one channel", n.Name)
		}
		if got := nodeName(a.Channels[0].Node); got != n.Node {
			t.Errorf("%s drives %q, the game's drives %q", n.Name, got, n.Node)
		}
		s := a.Samplers[a.Channels[0].Sampler]
		width := 3
		if n.Path == "rotation" {
			width = 4
		}
		if len(s.Input) != len(n.Times) || len(s.Output) != width*len(n.Times) {
			t.Fatalf("%s: %d keys, the game's %d", n.Name, len(s.Input), len(n.Times))
		}
		for i := range n.Times {
			if e := math.Abs(float64(s.Input[i]) - n.Times[i]); e > clipTolerance {
				t.Errorf("%s key %d time %v, the game's %v", n.Name, i, s.Input[i], n.Times[i])
			}
			for k := 0; k < width; k++ {
				w := n.Values[i][k]
				if n.Path == "translation" {
					w *= yardMetres
				}
				got := float64(s.Output[width*i+k])
				// A float32 near 58 m has a step of 4e-6, so a lookup value
				// is held to 1e-6 of its own size.
				if n.Path == "translation" && math.Abs(got-w) > clipTolerance*math.Max(1, math.Abs(w)) {
					t.Errorf("%s key %d [%d] = %v m, the game's %v", n.Name, i, k, got, w)
				}
			}
			if n.Path == "rotation" {
				var q [4]float32
				copy(q[:], s.Output[4*i:4*i+4])
				var w [4]float64
				copy(w[:], n.Values[i])
				if e := quatDistance(w, q); e > clipTolerance {
					t.Errorf("%s key %d rotation %v, the game's %v", n.Name, i, q, w)
				}
			}
		}
	}
}

// animationsDigest hashes the decoded animations: each name, each channel's
// node name and path, and its keys as written. It leaves the mesh out, so a
// change to the geometry kernel does not move it.
func animationsDigest(d *gltf.Document) string {
	h := sha256.New()
	put := func(v any) { _ = binary.Write(h, binary.LittleEndian, v) }
	for _, a := range d.Animations {
		h.Write([]byte(a.Name + "\x00"))
		for _, ch := range a.Channels {
			h.Write([]byte(d.Nodes[ch.Node].Name + "\x00" + ch.Path + "\x00"))
			s := a.Samplers[ch.Sampler]
			put(s.Input)
			put(s.Output)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// The library compiles to the same animations on every build and on every
// architecture: the digest was measured on amd64 and the test also runs on
// arm64.
func TestClipFixtureIsByteStable(t *testing.T) {
	a, da := buildClipFixture(t)
	b, db := buildClipFixture(t)
	if !bytes.Equal(a, b) {
		t.Fatal("two builds of the clip fixture differ")
	}
	want, err := os.ReadFile(filepath.FromSlash(clipDigest))
	if err != nil {
		t.Fatal(err)
	}
	got := animationsDigest(da)
	if got != animationsDigest(db) {
		t.Fatal("the animations of two builds differ")
	}
	if got != strings.TrimSpace(string(want)) {
		t.Errorf("the animations' digest is %s, the candidate pins %s (%s %s)", got, strings.TrimSpace(string(want)), runtime.GOOS, runtime.GOARCH)
	}
}

// clipProject builds a small project in memory: files maps a path to source.
// The asset is files["asset.gw"] and the project root is the map's root.
func clipProject(t testing.TB, files map[string]string) ([]diag.Diagnostic, *Product) {
	t.Helper()
	fsys := fstest.MapFS{}
	for name, src := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(src)}
	}
	f, ds := ast.LowerWith(lang.Parse([]byte(files["asset.gw"])), ast.Options{Project: &ast.Project{FS: fsys, Dir: ""}})
	if diag.HasErrors(ds) {
		return ds, nil
	}
	p, bs := Build(f, "")
	return bs, p
}

const clipTestAsset = `greenware 1
asset small {
  root = body
  idle = rest
  %s
}
%s
skeleton rig {
  joint hip { at = (0, 1, 0)  joint top { at = (0, 1.5, 0) } }
}
part body { joint = hip  ellipsoid blob { centre = (0, 1, 0) radii = (0.2, 0.2, 0.2) } }
clip rest { duration = 1s  keys = 2  pose { hip = rot(0, 0, 0)  root = (0, 1, 0) } }
output o { file = "small.glb" }
`

const clipTestLib = `greenware 1
let lift = 5deg
clip wave { duration = 1s  keys = 3  pose { top = rot(lift * t, 0, 0) } }
clip _helper { duration = 1s  keys = 2  pose { top = rot(1, 0, 0) } }
`

func TestUseClipsInstallsPublicClips(t *testing.T) {
	files := map[string]string{
		"asset.gw": strings.Replace(strings.Replace(clipTestAsset, "%s", "use_clips { of = lib }", 1), "%s", `import "lib.gw" as lib`, 1),
		"lib.gw":   clipTestLib,
	}
	ds, p := clipProject(t, files)
	if p == nil {
		t.Fatalf("does not build: %v", ds)
	}
	d, err := gltf.Decode(p.Outputs[0].Files[0].GLB)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range d.Animations {
		names = append(names, a.Name)
	}
	if got := strings.Join(names, " "); got != "rest wave" {
		t.Errorf("animations %q, want the local clip and the library's public clip", got)
	}
	// Importing alone installs nothing.
	files["asset.gw"] = strings.Replace(strings.Replace(clipTestAsset, "%s", "", 1), "%s", `import "lib.gw" as lib`, 1)
	if ds, p := clipProject(t, files); p != nil {
		d, _ := gltf.Decode(p.Outputs[0].Files[0].GLB)
		t.Errorf("an import with no use_clips built with %d animations", len(d.Animations))
	} else if len(ds) == 0 {
		t.Error("an import with no use_clips gave no diagnostic")
	}
}

func TestUseClipsRefusals(t *testing.T) {
	asset := func(use, imp string) string {
		return strings.Replace(strings.Replace(clipTestAsset, "%s", use, 1), "%s", imp, 1)
	}
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"a name that is not imported", map[string]string{"asset.gw": asset("use_clips { of = nope }", ""), "lib.gw": clipTestLib}, "is not an imported clip library"},
		{"a clip that repeats a local one", map[string]string{"asset.gw": asset("use_clips { of = lib }", `import "lib.gw" as lib`), "lib.gw": strings.Replace(clipTestLib, "clip wave", "clip rest", 1)}, "two clips export the animation name"},
		{"a library that declares an asset", map[string]string{"asset.gw": asset("use_clips { of = lib }", `import "lib.gw" as lib`), "lib.gw": asset("", "")}, "declares an asset"},
		{"a joint the skeleton lacks", map[string]string{"asset.gw": asset("use_clips { of = lib }", `import "lib.gw" as lib`), "lib.gw": strings.Replace(clipTestLib, "top =", "neck =", 1)}, "no joint is named `neck`"},
		{"a skeleton by another name", map[string]string{"asset.gw": asset("use_clips { of = lib skeleton = other }", `import "lib.gw" as lib`), "lib.gw": clipTestLib}, "skeleton is `rig`"},
		{"a library that declares a skeleton", map[string]string{"asset.gw": asset("use_clips { of = lib }", `import "lib.gw" as lib`), "lib.gw": clipTestLib + "skeleton s { joint a { at = (0, 0, 0) } }\n"}, "declares a skeleton"},
	}
	for _, c := range cases {
		ds, p := clipProject(t, c.files)
		if p != nil {
			t.Errorf("%s: built", c.name)
			continue
		}
		found := false
		for _, d := range ds {
			found = found || strings.Contains(d.Message, c.want)
		}
		if !found {
			t.Errorf("%s: want a diagnostic with %q, got %v", c.name, c.want, ds)
		}
	}
}
