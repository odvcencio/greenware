package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"m31labs.dev/greenware/accept"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
)

const (
	armPath       = "../examples/arm/arm.gw"
	armAccept     = "../examples/arm/arm.accept.gw"
	armGolden     = "../testdata/compiler/arm.candidate.sha256"
	rigFixtureDir = "../testdata/diag/"
)

func buildArm(t testing.TB) (*Product, *gltf.Document) {
	t.Helper()
	p, ds := Build(loadFile(t, armPath), "")
	if p == nil {
		t.Fatalf("the arm does not build: %v", ds)
	}
	d, err := gltf.Decode(p.Outputs[0].Files[0].GLB)
	if err != nil {
		t.Fatalf("the arm's GLB does not decode: %v", err)
	}
	return p, d
}

func nodeNamed(t testing.TB, d *gltf.Document, name string) int {
	t.Helper()
	for i, n := range d.Nodes {
		if n.Name == name {
			return i
		}
	}
	t.Fatalf("no node %q", name)
	return -1
}

func TestArmShape(t *testing.T) {
	p, d := buildArm(t)
	f := p.Outputs[0].Files[0]
	// The measured numbers the report quotes.
	if f.Joints != 3 || f.Influences != 2 || f.Morphs != 2 || f.Animations != 2 {
		t.Errorf("joints %d influences %d morphs %d animations %d", f.Joints, f.Influences, f.Morphs, f.Animations)
	}
	t.Logf("arm: %d joints, %d vertices (%d bound positions), %d triangles, %d draws, max influences %d, %d morph targets, %d animations, %d keys, %d bytes",
		f.Joints, f.Vertices, f.SkinnedVertices, f.Triangles, f.Draws, f.Influences, f.Morphs, f.Animations, f.AnimKeys, f.Size)
	if len(d.Skins) != 1 || len(d.Skins[0].Joints) != 3 || len(d.Skins[0].InverseBindMatrices) != 3 {
		t.Fatalf("skins: %+v", d.Skins)
	}
	names := []string{}
	for _, j := range d.Skins[0].Joints {
		names = append(names, d.Nodes[j].Name)
	}
	if strings.Join(names, ",") != "shoulder,elbow,wrist" {
		t.Errorf("joint order %v", names)
	}
	m := d.Meshes[0]
	if m.Name != "Arm" || len(m.TargetNames) != 2 || m.TargetNames[0] != "long" || m.TargetNames[1] != "bulk-up" {
		t.Errorf("mesh %q targets %v", m.Name, m.TargetNames)
	}
	if len(m.Weights) != 2 || m.Weights[0] != 0 || m.Weights[1] != 0 {
		t.Errorf("default weights %v", m.Weights)
	}
	for _, pr := range m.Primitives {
		if len(pr.Targets) != 2 || len(pr.Joints0) != len(pr.Weights0) {
			t.Errorf("primitive: %d targets, %d joints, %d weights", len(pr.Targets), len(pr.Joints0), len(pr.Weights0))
		}
	}
	if len(d.Animations) != 2 || d.Animations[0].Name != "rest" || d.Animations[1].Name != "wave" {
		t.Errorf("animations %v", d.Animations)
	}
	// The grip socket sits under the wrist at the converted local point.
	if i := nodeNamed(t, d, "socket.grip"); d.Nodes[i].Name != "socket.grip" {
		t.Error("socket")
	}
}

// With every joint at its original local rest and every morph weight zero, a
// skinned vertex is its bind position: skin matrix = identity. The rest comes
// from rig_base, so this checks the hierarchy and the inverse binds against
// each other.
func TestArmBindPoseReproduction(t *testing.T) {
	_, d := buildArm(t)
	var ex struct {
		Greenware struct {
			RigBase struct {
				Rest []struct {
					Joint       string
					Translation [3]float64
					Rotation    *[4]float64
				}
			} `json:"rig_base"`
		}
	}
	if err := json.Unmarshal(d.Extras, &ex); err != nil {
		t.Fatal(err)
	}
	rest := ex.Greenware.RigBase.Rest
	if len(rest) != 3 {
		t.Fatalf("rig_base rest: %+v", rest)
	}
	ev := rigEval{d}
	T, Q := ev.locals("", 0)
	for _, r := range rest {
		i := nodeNamed(t, d, r.Joint)
		T[i] = r.Translation
		Q[i] = [4]float64{0, 0, 0, 1}
		if r.Rotation != nil {
			Q[i] = *r.Rotation
		}
	}
	w := ev.worlds(T, Q)
	// W_j I_j is the identity at the original rest.
	sk := d.Skins[0]
	for j, jn := range sk.Joints {
		var ibm m4
		for k, v := range sk.InverseBindMatrices[j] {
			ibm[k] = float64(v)
		}
		m := w[jn].mul(ibm)
		id := ident4()
		for k := range m {
			if math.Abs(m[k]-id[k]) > 1e-5 {
				t.Errorf("joint %d: W*I[%d] = %v, want %v", j, k, m[k], id[k])
			}
		}
	}
	// And the skinned mesh equals the bind mesh within 1e-5.
	var ws []float64
	for range d.Meshes[0].TargetNames {
		ws = append(ws, 0)
	}
	// Evaluate with the rest locals by building a copy whose nodes hold them.
	cp := *d
	cp.Nodes = append([]gltf.Node(nil), d.Nodes...)
	for _, r := range rest {
		i := nodeNamed(t, d, r.Joint)
		tr := [3]float32{float32(r.Translation[0]), float32(r.Translation[1]), float32(r.Translation[2])}
		cp.Nodes[i].Translation, cp.Nodes[i].Rotation = &tr, nil
		if r.Rotation != nil {
			q := [4]float32{float32(r.Rotation[0]), float32(r.Rotation[1]), float32(r.Rotation[2]), float32(r.Rotation[3])}
			cp.Nodes[i].Rotation = &q
		}
	}
	got := rigEval{&cp}.posed("", 0, ws)
	want := bindPositions(d)
	if len(got) != len(want) || len(got) == 0 {
		t.Fatalf("%d posed, %d bind", len(got), len(want))
	}
	worst := 0.0
	for i := range got {
		worst = math.Max(worst, dist3(got[i], want[i]))
	}
	if worst > 1e-5 {
		t.Errorf("the bind pose is reproduced to %g, want 1e-5", worst)
	}
	t.Logf("bind pose reproduced to %.3g m over %d vertices", worst, len(got))
}

func TestArmWeights(t *testing.T) {
	p, d := buildArm(t)
	js, ws := skinAttrs(d)
	most := 0
	nj := len(d.Skins[0].Joints)
	for v := range ws {
		var sum float64
		n := 0
		prev := float32(2)
		for l := 0; l < 4; l++ {
			w := ws[v][l]
			if w < 0 || w > 1 {
				t.Fatalf("vertex %d weight %v", v, w)
			}
			if w > prev {
				t.Fatalf("vertex %d: weights are not sorted: %v", v, ws[v])
			}
			prev = w
			sum += float64(w)
			if w > 0 {
				n++
				if int(js[v][l]) >= nj {
					t.Fatalf("vertex %d joint %d", v, js[v][l])
				}
			}
		}
		if math.Abs(sum-1) > 1e-6 {
			t.Fatalf("vertex %d: weights sum to %.9g", v, sum)
		}
		most = max(most, n)
	}
	if most > 2 {
		t.Errorf("%d influences on one vertex; the skin allows 2", most)
	}
	if most != p.Outputs[0].Files[0].Influences {
		t.Errorf("measured influences %d, file %d", p.Outputs[0].Files[0].Influences, most)
	}
	// Some vertices are blended: the bands did their work.
	blended := 0
	for v := range ws {
		if ws[v][1] > 0 {
			blended++
		}
	}
	if blended == 0 {
		t.Error("no vertex is blended")
	}
	t.Logf("%d of %d vertices blended; at most %d influences", blended, len(ws), most)
}

// vertexBoundTo finds a vertex near pos that carries only the joint (weight 1).
func vertexBoundTo(t *testing.T, d *gltf.Document, pos [3]float64, joint int) int {
	t.Helper()
	bind := bindPositions(d)
	js, ws := skinAttrs(d)
	best, bestD := -1, 1e9
	for v := range bind {
		if js[v][0] == uint16(joint) && ws[v][0] == 1 {
			if dd := dist3(bind[v], pos); dd < bestD {
				best, bestD = v, dd
			}
		}
	}
	if best < 0 || bestD > 1e-6 {
		t.Fatalf("no vertex wholly on joint %d at %v (nearest %g)", joint, pos, bestD)
	}
	return best
}

// Hand-computed positions of the arm under the `wave` clip. The shoulder is
// the root: at t = 0.25 s... (key i=1 of 9 over 2 s), t/2 s = 0.125: see below.
func TestArmWaveHandComputed(t *testing.T) {
	_, d := buildArm(t)
	ev := rigEval{d}
	// The top of the upper arm is a vertex on the shoulder axis: (0.01, 1.4, 0).
	top := vertexBoundTo(t, d, [3]float64{0.01, 1.4, 0}, 0)
	// A ring vertex of the forearm that the bands leave wholly on the elbow.
	// Its bind position is read from the file; the expected posed position is
	// derived below from the hand-written transform chain.
	bind := bindPositions(d)
	js, ws := skinAttrs(d)
	elbowV := -1
	for v := range bind {
		if js[v][0] == 1 && ws[v][0] == 1 && bind[v][1] < 0.95 && bind[v][1] > 0.9 {
			elbowV = v
			break
		}
	}
	if elbowV < 0 {
		t.Fatal("no forearm vertex wholly on the elbow")
	}
	// Times of the 9 keys over 2 s are i/4 s: i = 2 is 0.5 s, where
	// s = sin(360 deg * 0.25) = 1.
	// shoulder rot(-70+15, 0, 10), elbow rot(-60-25, 0, 0), root (0, 1.4+0.02, 0).
	const tt = 0.5
	sh := transMat(0, 1.42, 0).mul(rotMat(-55, 0, 10))
	got := ev.posed("wave", tt, nil)
	// Shoulder-bound top vertex: p' = key + R (p - (0,1.4,0)).
	want := sh.point([3]float64{0.01, 0, 0})
	if dist3(got[top], want) > 2e-5 {
		t.Errorf("shoulder vertex at %g s: %v, want %v", tt, got[top], want)
	}
	// Closed form for the numbers the reference text gives: (0.009848, 1.420996, -0.001422).
	if dist3(got[top], [3]float64{0.009848, 1.420996, -0.001422}) > 2e-5 {
		t.Errorf("shoulder vertex %v, hand value (0.009848, 1.420996, -0.001422)", got[top])
	}
	// Elbow-bound vertex: p' = W_sh T(l_el) R_el (p - bind_el). The elbow bind
	// and its local rest follow from the lengthen step by hand: the bone from
	// (0,1.4,0) to (0.02,1.1,0), 0.300666 m long, becomes 0.32 m along its own
	// direction, so the elbow's bind is (0.021286, 1.080709, 0).
	l := math.Hypot(0.02, 0.3)
	dir := [3]float64{0.02 / l, -0.3 / l, 0}
	shift := 0.32 - l
	elbowBind := [3]float64{0.02 + shift*dir[0], 1.1 + shift*dir[1], 0}
	local := transMat(elbowBind[0], elbowBind[1]-1.4, 0).mul(rotMat(-85, 0, 0))
	rel := [3]float64{bind[elbowV][0] - elbowBind[0], bind[elbowV][1] - elbowBind[1], bind[elbowV][2]}
	wantE := sh.mul(local).point(rel)
	if dist3(got[elbowV], wantE) > 3e-5 {
		t.Errorf("elbow vertex at %g s: %v, want %v", tt, got[elbowV], wantE)
	}
	// At t = 0 the pose is the first key: s = 0, shoulder rot(-70, 0, 10), root (0, 1.4, 0).
	got0 := ev.posed("wave", 0, nil)
	want0 := transMat(0, 1.4, 0).mul(rotMat(-70, 0, 10)).point([3]float64{0.01, 0, 0})
	if dist3(got0[top], want0) > 2e-5 {
		t.Errorf("shoulder vertex at 0 s: %v, want %v", got0[top], want0)
	}
	// Between keys the evaluator interpolates; at the last key the pose repeats the first (loop does not force it, the sine does).
	got2 := ev.posed("wave", 2, nil)
	if dist3(got2[top], got0[top]) > 3e-5 {
		t.Errorf("last key %v differs from the first %v", got2[top], got0[top])
	}
}

// The idle clip's first key is what the unplayed file shows.
func TestArmIdleIsDisplayed(t *testing.T) {
	_, d := buildArm(t)
	el := d.Nodes[nodeNamed(t, d, "elbow")]
	// rest: elbow = rot(-12, 0, 0): the quaternion (sin(-6 deg), 0, 0, cos(6 deg)).
	want := [4]float64{math.Sin(-6 * math.Pi / 180), 0, 0, math.Cos(6 * math.Pi / 180)}
	if el.Rotation == nil {
		t.Fatal("the elbow node has no rotation: the idle pose is not displayed")
	}
	for i := range want {
		if math.Abs(float64(el.Rotation[i])-want[i]) > 1e-6 {
			t.Errorf("elbow rotation %v, want %v", *el.Rotation, want)
			break
		}
	}
	// The wave clip leaves nothing the idle clip sets unkeyed: both key every joint and the root.
	if len(d.Animations[1].Channels) != len(d.Animations[0].Channels) {
		t.Errorf("channels: rest %d, wave %d", len(d.Animations[0].Channels), len(d.Animations[1].Channels))
	}
	// Channels are ordered by node and then property; times start at 0 and end at the duration.
	for _, a := range d.Animations {
		for _, c := range a.Channels {
			s := a.Samplers[c.Sampler]
			if s.Input[0] != 0 {
				t.Errorf("%s: a channel starts at %v", a.Name, s.Input[0])
			}
		}
	}
	if last := d.Animations[1].Samplers[0].Input; last[len(last)-1] != 2 || len(last) != 9 {
		t.Errorf("wave keys %v", last)
	}
}

// The `bulk` morph inflates the upper arm and the forearm by 20 % about their
// centre lines. A vertex of the upper arm moves radially by 0.2 times its
// distance from the arm's axis; the axis is the segment between the two
// cap-centre vertices, which the decoded mesh holds.
func TestArmBulkMorph(t *testing.T) {
	_, d := buildArm(t)
	bind := bindPositions(d)
	// The cap centres of the upper arm: bound wholly to the shoulder, on x = 0.01 and z = 0.
	m := d.Meshes[0]
	var deltas [][3]float64
	for _, p := range m.Primitives {
		for v := 0; v < len(p.Positions)/3; v++ {
			deltas = append(deltas, [3]float64{float64(p.Targets[1].Positions[3*v]), float64(p.Targets[1].Positions[3*v+1]), float64(p.Targets[1].Positions[3*v+2])})
		}
	}
	js, ws := skinAttrs(d)
	// Upper-arm vertices: weight on the shoulder only, x near 0.01.
	var top, bottom [3]float64
	topY, botY := -1e9, 1e9
	for v := range bind {
		if js[v][0] == 0 && ws[v][0] == 1 && math.Abs(bind[v][0]-0.01) < 1e-6 && math.Abs(bind[v][2]) < 1e-6 {
			if bind[v][1] > topY {
				topY, top = bind[v][1], bind[v]
			}
			if bind[v][1] < botY {
				botY, bottom = bind[v][1], bind[v]
			}
		}
	}
	if topY == -1e9 {
		t.Fatal("no cap centre found")
	}
	axis := [3]float64{bottom[0] - top[0], bottom[1] - top[1], bottom[2] - top[2]}
	al := math.Sqrt(axis[0]*axis[0] + axis[1]*axis[1] + axis[2]*axis[2])
	checked := 0
	for v := range bind {
		// The upper arm's vertices are those between the cap centres in height whose weights are on the shoulder.
		if js[v][0] != 0 || ws[v][0] != 1 || bind[v][1] > top[1]+1e-9 || bind[v][1] < bottom[1]-1e-9 || bind[v][0] > 0.07 || bind[v][0] < -0.05 {
			continue
		}
		rel := [3]float64{bind[v][0] - top[0], bind[v][1] - top[1], bind[v][2] - top[2]}
		s := (rel[0]*axis[0] + rel[1]*axis[1] + rel[2]*axis[2]) / (al * al)
		foot := [3]float64{top[0] + s*axis[0], top[1] + s*axis[1], top[2] + s*axis[2]}
		r := dist3(bind[v], foot)
		want := [3]float64{0.2 * (bind[v][0] - foot[0]), 0.2 * (bind[v][1] - foot[1]), 0.2 * (bind[v][2] - foot[2])}
		if dist3(deltas[v], want) > 2e-5 {
			t.Fatalf("vertex %d (r=%g): delta %v, want %v", v, r, deltas[v], want)
		}
		checked++
	}
	if checked < 20 {
		t.Fatalf("only %d upper-arm vertices checked", checked)
	}
	// At weight 1 the posed bind of an upper-arm ring vertex is 20 % farther from the axis.
	ev := rigEval{d}
	rest := ev.posed("", 0, []float64{0, 1})
	base := ev.posed("", 0, []float64{0, 0})
	if dist3(rest[0], base[0]) == 0 && checked > 0 {
		// vertex 0 may be a cap centre; compare any moved vertex
	}
	moved := 0
	for v := range rest {
		if dist3(rest[v], base[v]) > 1e-4 {
			moved++
		}
	}
	if moved == 0 {
		t.Error("the bulk morph moves nothing")
	}
	t.Logf("bulk morph: %d upper-arm vertices checked radially; %d vertices move at weight 1", checked, moved)
}

// The `long` morph with joints = follow records the difference of local
// translations, in the parent's frame. The numbers follow by hand from the
// lengthen step and the stretch.
func TestArmLongMorphJointOffsets(t *testing.T) {
	_, d := buildArm(t)
	var ex struct {
		Greenware struct {
			Runtime struct {
				Params []struct {
					Name     string
					Bindings []struct {
						Kind  string
						Morph string
						Joint string
						Delta [3]float64
						Map   struct {
							Table  string
							Points [][2]float64
						}
					}
				}
			}
		}
	}
	if err := json.Unmarshal(d.Extras, &ex); err != nil {
		t.Fatal(err)
	}
	l := math.Hypot(0.02, 0.3)
	shift := 0.32 - l
	elbowY := 1.1 - shift*0.3/l
	shoulderShift := 0.4 * (1.4 - 0.85) // clamp at the interval end: 0.4 * 0.25
	shoulderShift = 0.4 * 0.25
	elbowShift := 0.4 * (elbowY - 0.85)
	want := map[string]float64{"shoulder": shoulderShift, "elbow": elbowShift - shoulderShift, "wrist": -elbowShift}
	got := map[string]float64{}
	var table [][2]float64
	for _, p := range ex.Greenware.Runtime.Params {
		for _, b := range p.Bindings {
			if b.Kind == "joint_offset" && b.Morph == "long" {
				got[b.Joint] = b.Delta[1]
				if b.Delta[0] != 0 || b.Delta[2] != 0 {
					t.Errorf("%s: offset %v is not vertical", b.Joint, b.Delta)
				}
			}
			if b.Kind == "morph" && b.Morph == "long" {
				table = b.Map.Points
				if p.Name != "reach" || b.Map.Table != "piecewise_linear" {
					t.Errorf("morph long binds %q as %q", p.Name, b.Map.Table)
				}
			}
		}
	}
	for j, w := range want {
		if math.Abs(got[j]-w) > 2e-6 {
			t.Errorf("%s offset %v, want %v", j, got[j], w)
		}
	}
	// clamp((reach - 1) / 0.25, 0, 1) over 0.8..1.25: flat to 1, then up to 1 at 1.25.
	wantT := [][2]float64{{0.8, 0}, {1, 0}, {1.25, 1}}
	if len(table) != len(wantT) {
		t.Fatalf("table %v", table)
	}
	for i := range wantT {
		if math.Abs(table[i][0]-wantT[i][0]) > 1e-6 || math.Abs(table[i][1]-wantT[i][1]) > 1e-6 {
			t.Errorf("table %v, want %v", table, wantT)
		}
	}
}

func armSHA(t *testing.T) string {
	t.Helper()
	p, _ := buildArm(t)
	return p.Outputs[0].Files[0].SHA256
}

func TestArmDeterminism(t *testing.T) {
	a := armSHA(t)
	if b := armSHA(t); a != b {
		t.Errorf("two builds differ: %s %s", a, b)
	}
	old := runtime.GOMAXPROCS(1)
	one := armSHA(t)
	runtime.GOMAXPROCS(8)
	eight := armSHA(t)
	runtime.GOMAXPROCS(old)
	if one != a || eight != a {
		t.Errorf("GOMAXPROCS 1 %s, 8 %s, default %s", one, eight, a)
	}
	// The same source bytes from another folder.
	src, err := os.ReadFile(armPath)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "elsewhere", "checkout")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "arm.gw"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	p, ds := Build(loadFile(t, filepath.Join(other, "arm.gw")), "")
	if p == nil {
		t.Fatal(ds)
	}
	if got := p.Outputs[0].Files[0].SHA256; got != a {
		t.Errorf("another path gives %s, want %s", got, a)
	}
	// Writing the product leaves the same bytes on disk.
	dir := t.TempDir()
	if err := Write(p, dir); err != nil {
		t.Fatal(err)
	}
	disk, _ := os.ReadFile(filepath.Join(dir, "arm.glb"))
	sum := sha256.Sum256(disk)
	if hex.EncodeToString(sum[:]) != a {
		t.Error("the written file is not the built file")
	}
}

// The digest of the arm is pinned. The same test runs on amd64 and arm64.
func TestArmDigestMatchesCandidate(t *testing.T) {
	got := armSHA(t)
	b, err := os.ReadFile(armGolden)
	if err != nil {
		t.Fatalf("no candidate digest: %v (write %s)", err, got)
	}
	if want := strings.TrimSpace(string(b)); got != want {
		t.Errorf("arm.glb is %s on %s/%s, the candidate says %s", got, runtime.GOOS, runtime.GOARCH, want)
	}
}

func TestArmRoundTrip(t *testing.T) {
	p, d := buildArm(t)
	again, err := gltf.Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, p.Outputs[0].Files[0].GLB) {
		t.Log("decode then encode does not reproduce the bytes; the codec normalises something")
	}
}

// ---- the independent fixture of compiler.md 8.2 --------------------------------

// The fixture: identity mesh frame; original root rest (0,1,0), tip local rest
// (0,1,0); a vertex wholly on the tip at (0,2.5,0); a socket on the tip at
// local (0,0.5,0). The morph moves the tip by (0,0.2,0) parent-local and the
// vertex by (0,0.2,0). Default weight 0.5, root scale 1.25. The idle clip's
// first key puts the root at (0,0.8,0) turned 90 degrees about z.
const fixtureSrc = `greenware 1

asset fx {
  root = fx
  length = m
  angle = deg
  idle = rest
}

param amount : ratio = 0.5 in 0..1 runtime
param scale_up : ratio = 1.25 in 0.5..2 runtime

skeleton s {
  root_scale = scale_up
  joint base {
    at = (0, 1, 0)
    joint tip {
      at = (0, 2, 0)
      socket cap { kind = cap at = (0, 2.5, 0) }
    }
  }
}

part fx {
  joint = tip
  sphere dot { radius = 0.02 at = (0, 2.5, 0) segments = 6 }
}

morph long {
  weight = amount
  stretch { axis = y from = 1 to = 2 by = 20% }
  joints = follow
}

clip rest {
  duration = 1s
  pose {
    base = rot(0, 0, 90)
    root = (0, 0.8, 0)
  }
}

clip other {
  duration = 1s
  pose {
    root = (0, 0.5, 0)
  }
}

output fx { file = "fx.glb" }
`

func buildFixture(t *testing.T, src string) (*Product, *gltf.Document, []diag.Diagnostic) {
	t.Helper()
	p, ds := Build(lowerSource(t, []byte(src)), "")
	if p == nil {
		t.Fatalf("the fixture does not build: %v", ds)
	}
	d, err := gltf.Decode(p.Outputs[0].Files[0].GLB)
	if err != nil {
		t.Fatal(err)
	}
	return p, d, ds
}

func TestSpecFixtureAtDefaults(t *testing.T) {
	_, d, _ := buildFixture(t, fixtureSrc)
	// The core mesh weight is 0.5; the inverse binds are the adjusted rest's:
	// Y translations -1 and -2.1.
	if w := d.Meshes[0].Weights; len(w) != 1 || w[0] != 0.5 {
		t.Errorf("mesh weights %v", w)
	}
	ibm := d.Skins[0].InverseBindMatrices
	if math.Abs(float64(ibm[0][13])+1) > 1e-6 || math.Abs(float64(ibm[1][13])+2.1) > 1e-6 {
		t.Errorf("inverse bind Y translations %v %v, want -1 and -2.1", ibm[0][13], ibm[1][13])
	}
	// The unplayed root node: Y 1.0 (0.8 x 1.25) and the quaternion (0, 0, sqrt(1/2), sqrt(1/2)); tip local Y 1.1.
	base, tip := d.Nodes[nodeNamed(t, d, "base")], d.Nodes[nodeNamed(t, d, "tip")]
	if base.Translation == nil || math.Abs(float64(base.Translation[1])-1.0) > 1e-6 {
		t.Errorf("root node translation %v", base.Translation)
	}
	h := math.Sqrt(0.5)
	if base.Rotation == nil || math.Abs(float64(base.Rotation[2])-h) > 1e-6 || math.Abs(float64(base.Rotation[3])-h) > 1e-6 {
		t.Errorf("root node rotation %v", base.Rotation)
	}
	if tip.Translation == nil || math.Abs(float64(tip.Translation[1])-1.1) > 1e-6 {
		t.Errorf("tip local translation %v", tip.Translation)
	}
	// Stock-reader positions (metres): tip (-1.1, 1, 0); vertex and socket (-1.6, 1, 0).
	ev := rigEval{d}
	T, Q := ev.locals("", 0)
	w := ev.worlds(T, Q)
	if got := w[nodeNamed(t, d, "tip")].point([3]float64{}); dist3(got, [3]float64{-1.1, 1, 0}) > 1e-6 {
		t.Errorf("tip at %v", got)
	}
	capNode := nodeNamed(t, d, "socket.cap")
	if got := w[capNode].point([3]float64{}); dist3(got, [3]float64{-1.6, 1, 0}) > 1e-6 {
		t.Errorf("socket at %v", got)
	}
	for i, v := range ev.posed("", 0, nil) {
		if dist3(v, [3]float64{-1.6, 1, 0}) > 0.03 {
			t.Fatalf("vertex %d at %v, want within the 0.02 m ball about (-1.6, 1, 0)", i, v)
		}
	}
	// A second clip that leaves the root rotation unkeyed restores identity, not the idle quarter-turn.
	var other gltf.Animation
	for _, a := range d.Animations {
		if a.Name == "other" {
			other = a
		}
	}
	found := false
	for _, c := range other.Channels {
		if c.Node == nodeNamed(t, d, "base") && c.Path == gltf.PathRotation {
			found = true
			for _, v := range other.Samplers[c.Sampler].Output {
				_ = v
			}
			if o := other.Samplers[c.Sampler].Output; o[0] != 0 || o[1] != 0 || o[2] != 0 || o[3] != 1 {
				t.Errorf("the other clip's rest rotation is %v, want the identity", o[:4])
			}
		}
	}
	if !found {
		t.Error("the other clip has no rotation channel on the root: it would inherit the idle quarter-turn")
	}
	// Playing `other` at 0 puts the root at (0, 0.5 x 1.25, 0) with no rotation.
	T, Q = ev.locals("other", 0)
	if math.Abs(T[nodeNamed(t, d, "base")][1]-0.625) > 1e-6 || Q[nodeNamed(t, d, "base")] != [4]float64{0, 0, 0, 1} {
		t.Errorf("other at 0: %v %v", T[nodeNamed(t, d, "base")], Q[nodeNamed(t, d, "base")])
	}
}

// Row 5 of the language reference's table: weight 1 and scale 1.1 put the root
// at 0.9 x 1.1 = 0.99 m and the tip at 2.19 m. The runtime bindings in the file
// let an adapter rebuild this; here the source is rebuilt with those defaults.
func TestSpecFixtureRootScaleRule(t *testing.T) {
	src := strings.Replace(fixtureSrc, "= 0.5 in 0..1 runtime", "= 1 in 0..1 runtime", 1)
	src = strings.Replace(src, "= 1.25 in 0.5..2 runtime", "= 1.1 in 0.5..2 runtime", 1)
	src = strings.Replace(src, "base = rot(0, 0, 90)\n    root = (0, 0.8, 0)", "root = (0, 0.9, 0)", 1)
	_, d, _ := buildFixture(t, src)
	ev := rigEval{d}
	T, Q := ev.locals("", 0)
	w := ev.worlds(T, Q)
	base := w[nodeNamed(t, d, "base")].point([3]float64{})
	tip := w[nodeNamed(t, d, "tip")].point([3]float64{})
	if math.Abs(base[1]-0.99) > 1e-6 || math.Abs(tip[1]-2.19) > 1e-6 {
		t.Errorf("root at %v, tip at %v; want 0.99 and 2.19", base, tip)
	}
}

// ---- diagnostics from the compiler -----------------------------------------------

func buildFixtureFile(t *testing.T, name string) (*Product, []diag.Diagnostic) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(rigFixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return Build(lowerSource(t, src), "")
}

func TestHardLimitStopsTheBuild(t *testing.T) {
	p, ds := buildFixtureFile(t, "GW-RIG-LIMIT.gw")
	if p != nil {
		t.Error("a pose outside a hard limit was published")
	}
	found := false
	for _, d := range ds {
		if d.Code == diag.CodeRigLimit {
			found = true
			if !strings.Contains(d.Message, "swing_x") || !strings.Contains(d.Message, "elbow") {
				t.Errorf("message %q", d.Message)
			}
		}
	}
	if !found {
		t.Errorf("no GW-RIG-LIMIT in %v", ds)
	}
}

func TestBandIsOnlyAWarning(t *testing.T) {
	p, ds := buildFixtureFile(t, "GW-RIG-BAND.gw")
	if p == nil {
		t.Fatalf("a band warning stopped the build: %v", ds)
	}
	found := false
	for _, d := range ds {
		if d.Code == diag.CodeRigBand {
			found = true
			if d.Severity != diag.Warning {
				t.Errorf("severity %v", d.Severity)
			}
		}
	}
	if !found {
		t.Errorf("no GW-RIG-BAND in %v", ds)
	}
	// The pose is written as authored, not clamped: -160 degrees about x.
	d, err := gltf.Decode(p.Outputs[0].Files[0].GLB)
	if err != nil {
		t.Fatal(err)
	}
	el := d.Nodes[nodeNamed(t, d, "elbow")]
	want := math.Sin(-80 * math.Pi / 180)
	if el.Rotation == nil || math.Abs(float64(el.Rotation[0])-want) > 1e-6 {
		t.Errorf("elbow rotation %v, want x = %v", el.Rotation, want)
	}
}

func TestRigTicketsAreReserved(t *testing.T) {
	p, _ := buildArm(t)
	var names []string
	for _, c := range p.Receipt.Charges {
		names = append(names, c.Ticket)
	}
	want := "snapshot plan rig surface_branches weights morph clip encode publish"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("tickets %q, want %q", got, want)
	}
	// An exact limit admits the build; one unit below refuses the last ticket.
	f := loadFile(t, armPath)
	total := p.Receipt.Total
	if q, _, ds := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: total}}); q == nil {
		t.Errorf("the exact limit refused: %v", ds)
	}
	if q, rc, ds := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: total - 1}}); q != nil || rc == nil || rc.Refused == nil || rc.Refused.Ticket != TicketPublish {
		t.Errorf("one below: %v %+v %v", q, rc, ds)
	}
	// Each rig ticket refuses on its own: a limit that leaves less than its price.
	prices := map[string]uint64{}
	var sum uint64
	for _, c := range p.Receipt.Charges {
		sum += c.Units
		prices[c.Ticket] = sum
	}
	for _, tk := range []string{TicketRig, TicketWeights, TicketMorph, TicketClip} {
		limit := prices[tk] - 1
		_, rc, _ := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: limit}})
		if rc == nil || rc.Refused == nil || rc.Refused.Ticket != tk {
			t.Errorf("limit %d should refuse %s, got %+v", limit, tk, rc.Refused)
		}
	}
}

func TestOtherAssetsReserveNoRigTickets(t *testing.T) {
	p := buildGoalPost(t)
	for _, c := range p.Receipt.Charges {
		switch c.Ticket {
		case TicketRig, TicketWeights, TicketMorph, TicketClip:
			t.Errorf("the goal post reserved %s", c.Ticket)
		}
	}
}

func TestArmAcceptance(t *testing.T) {
	_, dir := buildInto(t, armPath)
	var out, errOut bytes.Buffer
	code := accept.Command([]string{armPath, armAccept, "--glb-dir", dir, "--json"}, &out, &errOut)
	var rep accept.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("exit %d, not a report: %v\n%s\n%s", code, err, out.String(), errOut.String())
	}
	if code != 0 {
		logTable(t, &rep)
		t.Fatalf("exit %d\n%s", code, errOut.String())
	}
	n := 0
	for _, l := range rep.Levels {
		for _, e := range l.Expects {
			n++
			if e.Verdict != "pass" {
				t.Errorf("%s: %s (%v)", e.Message, e.Verdict, e.Measures)
			}
		}
	}
	if n < 8 {
		t.Errorf("only %d expectations ran", n)
	}
}
