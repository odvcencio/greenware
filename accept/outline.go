package accept

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"io/fs"
	"math"
	"strconv"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/units"
)

const (
	outlineSchema    = "greenware.outline/1"
	maxOutlineSide   = 2048
	maxRasterSamples = 64 * 1024 * 1024
	subpixels        = 256
)

type outlineCamera struct {
	Schema string        `json:"schema"`
	View   string        `json:"view"`
	Width  int           `json:"width"`
	Height int           `json:"height"`
	Eye    outlineMatrix `json:"model_to_eye"`
	Clip   outlineMatrix `json:"eye_to_clip"`
}

type outlineMatrix []float64

func (m *outlineMatrix) UnmarshalJSON(data []byte) error {
	var values []*float64
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	if len(values) != 16 {
		return errors.New("camera matrix must have 16 numbers")
	}
	matrix := make(outlineMatrix, len(values))
	for i, value := range values {
		if value == nil {
			return fmt.Errorf("camera matrix element %d must be a number", i)
		}
		matrix[i] = *value
	}
	*m = matrix
	return nil
}

type outlineInput struct {
	camera   outlineCamera
	boundary []bool
	maskSHA  [sha256.Size]byte
	viewSHA  [sha256.Size]byte
	err      error
}

// The first read fixes the reference and camera for all levels in this run.
type outlineRefs struct {
	fs     fs.FS
	loaded map[string]outlineInput
}

func (r *outlineRefs) input(name string) outlineInput {
	if in, ok := r.loaded[name]; ok {
		return in
	}
	in, err := readOutline(r.fs, name)
	in.err = err
	r.loaded[name] = in
	return in
}

func readOutline(fsys fs.FS, name string) (outlineInput, error) {
	var in outlineInput
	mask, err := readOutlineFile(fsys, name, 16<<20)
	if err != nil {
		return in, err
	}
	camera, err := readOutlineFile(fsys, name+".view.json", 16<<10)
	if err != nil {
		return in, err
	}
	c := &in.camera
	if err := json.Unmarshal(camera, c); err != nil {
		return in, fmt.Errorf("camera: %w", err)
	}
	if c.Schema != outlineSchema || c.View == "" || len(c.Eye) != 16 || len(c.Clip) != 16 {
		return in, errors.New("camera needs the greenware.outline/1 schema, a view and two 16-value matrices")
	}
	if c.Width < 1 || c.Height < 1 || c.Width > maxOutlineSide || c.Height > maxOutlineSide {
		return in, fmt.Errorf("camera width and height must be within 1–%d pixels", maxOutlineSide)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(mask))
	if err != nil {
		return in, fmt.Errorf("mask: %w", err)
	}
	if cfg.Width != c.Width || cfg.Height != c.Height {
		return in, errors.New("mask size differs from camera size")
	}
	img, err := png.Decode(bytes.NewReader(mask))
	if err != nil {
		return in, fmt.Errorf("mask: %w", err)
	}
	pixels := make([]bool, c.Width*c.Height)
	for y := range c.Height {
		for x := range c.Width {
			r, g, b, a := img.At(x, y).RGBA()
			if a != 0xffff || r != g || r != b || (r != 0 && r != 0xffff) {
				return in, errors.New("mask must contain only opaque black and white pixels")
			}
			pixels[y*c.Width+x] = r == 0xffff
		}
	}
	in.boundary = maskBoundary(pixels, c.Width, c.Height)
	in.maskSHA = sha256.Sum256(mask)
	in.viewSHA = sha256.Sum256(camera)
	return in, nil
}

func readOutlineFile(fsys fs.FS, name string, limit int64) ([]byte, error) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%q must be a regular file of at most %d bytes", name, limit)
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%q exceeds %d bytes", name, limit)
	}
	return b, err
}

func (ev *evaluator) outline(c *ast.Call) (val, *evalErr) {
	args, err := bind(c, "view", "file", "within")
	if err != nil {
		return val{}, err
	}
	view, viewOK := unparen(args[0]).(*ast.Name)
	lit, fileOK := unparen(args[1]).(*ast.StringLit)
	if !viewOK || !fileOK || args[2] == nil {
		return val{}, refuse("outline needs a view name, a reference path and a pixel tolerance")
	}
	name := strings.NewReplacer("{output}", ev.output, "{lod}", strconv.Itoa(ev.lod)).Replace(lit.Value)
	if !fs.ValidPath(name) || !strings.HasSuffix(name, ".png") || strings.ContainsAny(name, "\\:") {
		return val{}, refuse("invalid outline reference path %q", name)
	}
	tolerance, err := ev.eval(args[2])
	if err != nil {
		return val{}, err
	}
	if u := tolerance.u; u.Kind != units.KNumber || u.Dim != units.Pixels || u.Num < 0 || math.IsNaN(u.Num) || math.IsInf(u.Num, 0) {
		return val{}, refuse("outline tolerance must be a nonnegative pixel distance")
	}
	if ev.outlineRefs == nil {
		return val{}, unavailable("outline needs reference mask %q and its camera sidecar", name)
	}
	ref := ev.outlineRefs.input(name)
	if errors.Is(ref.err, fs.ErrNotExist) {
		return val{}, unavailable("outline needs reference mask %q and its camera sidecar: %v", name, ref.err)
	}
	if ref.err != nil {
		return val{}, refuse("outline reference %q: %v", name, ref.err)
	}
	if view.Name != ref.camera.View {
		return val{}, refuse("outline view %q differs from camera view %q", view.Name, ref.camera.View)
	}
	rendered, renderErr := renderOutline(ev.sc, ref.camera)
	if renderErr != nil {
		return val{}, refuse("outline: %v", renderErr)
	}
	matched, total := scoreBoundary(ref.boundary, rendered, ref.camera.Width, ref.camera.Height, tolerance.u.Num)
	if total == 0 {
		return val{}, refuse("outline reference %q has an empty boundary", name)
	}
	score := float64(matched) / float64(total)
	ev.record("outline mask sha256", fmt.Sprintf("%x", ref.maskSHA), nil)
	ev.record("outline camera sha256", fmt.Sprintf("%x", ref.viewSHA), nil)
	ev.record("outline boundary match", fmt.Sprintf("%d of %d reference pixels", matched, total), &score)
	return uval(units.Num(score)), nil
}

func renderOutline(sc *scene, camera outlineCamera) ([]bool, error) {
	mask := make([]bool, camera.Width*camera.Height)
	budget := maxRasterSamples
	for _, i := range sc.allVisible() {
		n := sc.doc.Nodes[i]
		if n.Mesh == nil {
			continue
		}
		if n.Skin != nil {
			return nil, errors.New("locked-camera silhouettes support rigid meshes only")
		}
		world := sc.nodes[i].world
		for _, primitive := range sc.doc.Meshes[*n.Mesh].Primitives {
			if len(primitive.Targets) != 0 {
				return nil, errors.New("locked-camera silhouettes support rigid meshes only")
			}
			var material gltf.Material
			if primitive.Material != nil {
				material = sc.doc.Materials[*primitive.Material]
			}
			switch material.AlphaMode {
			case "", gltf.AlphaOpaque:
			case gltf.AlphaBlend:
				if material.BaseColor[3] <= 0 {
					continue
				}
			default:
				return nil, fmt.Errorf("unsupported alpha mode %q", material.AlphaMode)
			}
			for _, triangle := range appendPrimitive(nil, primitive, world, world.det() < 0) {
				clipped, err := projectTriangle(triangle, camera)
				if err != nil {
					return nil, err
				}
				for j := 1; j+1 < len(clipped); j++ {
					budget -= paintTriangle(mask, camera.Width, camera.Height, clipped[0], clipped[j], clipped[j+1], material.DoubleSided, budget)
					if budget < 0 {
						return nil, errors.New("raster sample limit exceeded")
					}
				}
			}
		}
	}
	return maskBoundary(mask, camera.Width, camera.Height), nil
}

type vec4 [4]float64

func mul4(m []float64, v vec4) vec4 {
	var result vec4
	for row := range result {
		result[row] = float64(m[row]*v[0]) + float64(m[4+row]*v[1]) +
			float64(m[8+row]*v[2]) + float64(m[12+row]*v[3])
	}
	return result
}

type pixelPoint struct{ x, y int64 }

func projectTriangle(triangle tri, camera outlineCamera) ([]pixelPoint, error) {
	var vertices [3]vec4
	for i, p := range triangle {
		v := mul4(camera.Clip, mul4(camera.Eye, vec4{p[0], p[1], p[2], 1}))
		for _, a := range v {
			if math.IsNaN(a) || math.Abs(a) > math.MaxFloat64/4 {
				return nil, errors.New("camera projection produced a non-finite vertex")
			}
		}
		vertices[i] = v
	}
	poly := vertices[:]
	for plane := range 6 {
		if len(poly) == 0 {
			break
		}
		poly = clipPlane(poly, plane)
	}
	out := make([]pixelPoint, 0, len(poly))
	for _, v := range poly {
		if v[3] <= 0 {
			continue
		}
		x := (1 + v[0]/v[3]) * float64(camera.Width) * (subpixels / 2)
		y := (1 - v[1]/v[3]) * float64(camera.Height) * (subpixels / 2)
		out = append(out, pixelPoint{
			x: int64(math.RoundToEven(min(max(x, 0), float64(camera.Width*subpixels)))),
			y: int64(math.RoundToEven(min(max(y, 0), float64(camera.Height*subpixels)))),
		})
	}
	return out, nil
}

func clipPlane(poly []vec4, plane int) []vec4 {
	distance := func(p vec4) float64 {
		if plane%2 == 0 {
			return p[3] + p[plane/2]
		}
		return p[3] - p[plane/2]
	}
	out := make([]vec4, 0, len(poly)+1)
	prev := poly[len(poly)-1]
	pd := distance(prev)
	for _, next := range poly {
		nd := distance(next)
		if (pd < 0) != (nd < 0) {
			t := pd / (pd - nd)
			var cross vec4
			for i := range cross {
				cross[i] = prev[i] + float64(t*(next[i]-prev[i]))
			}
			out = append(out, cross)
		}
		if nd >= 0 {
			out = append(out, next)
		}
		prev, pd = next, nd
	}
	return out
}

func edge(a, b, p pixelPoint) int64 {
	return (b.x-a.x)*(p.y-a.y) - (b.y-a.y)*(p.x-a.x)
}

func edgeIncludes(a, b, p pixelPoint) bool {
	e := edge(a, b, p)
	return e > 0 || (e == 0 && (b.y < a.y || (b.y == a.y && b.x > a.x)))
}

func paintTriangle(mask []bool, w, h int, a, b, c pixelPoint, doubleSided bool, budget int) int {
	area := edge(a, b, c)
	if area == 0 || (area > 0 && !doubleSided) {
		return 0
	}
	if area < 0 {
		b, c = c, b
	}
	x0 := max(0, int(min(a.x, b.x, c.x)/subpixels))
	x1 := min(w-1, int(max(a.x, b.x, c.x)/subpixels))
	y0 := max(0, int(min(a.y, b.y, c.y)/subpixels))
	y1 := min(h-1, int(max(a.y, b.y, c.y)/subpixels))
	samples := max(0, x1-x0+1) * max(0, y1-y0+1)
	if samples > budget {
		return samples
	}
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			p := pixelPoint{int64(x)*subpixels + subpixels/2, int64(y)*subpixels + subpixels/2}
			if edgeIncludes(a, b, p) && edgeIncludes(b, c, p) && edgeIncludes(c, a, p) {
				mask[y*w+x] = true
			}
		}
	}
	return samples
}

func maskBoundary(mask []bool, w, h int) []bool {
	boundary := make([]bool, len(mask))
	for y := range h {
		for x := range w {
			i := y*w + x
			boundary[i] = mask[i] && (x == 0 || x == w-1 || y == 0 || y == h-1 ||
				!mask[i-1] || !mask[i+1] || !mask[i-w] || !mask[i+w])
		}
	}
	return boundary
}

// scoreBoundary finds the squared Euclidean distance to the closest rendered
// boundary pixel. First find the closest pixel in each row. Then combine rows
// by taking the lower envelope of their parabolas in each column.
func scoreBoundary(ref, rendered []bool, w, h int, tolerance float64) (matched, total int) {
	inf := (w + h) * (w + h)
	horizontal := make([]int32, w*h)
	for y := range h {
		offset := y * w
		last := -w
		for x := range w {
			if rendered[offset+x] {
				last = x
			}
			d := x - last
			horizontal[offset+x] = int32(d * d)
		}
		last = 2 * w
		for x := w - 1; x >= 0; x-- {
			if rendered[offset+x] {
				last = x
			}
			d := last - x
			best := min(int(horizontal[offset+x]), d*d)
			if best >= w*w {
				best = inf
			}
			horizontal[offset+x] = int32(best)
		}
	}
	rows := make([]int, h)
	starts := make([]int, h)
	limit := tolerance * tolerance
	for x := range w {
		n := 0
		for y := range h {
			cost := int(horizontal[y*w+x])
			if cost == inf {
				continue
			}
			start := 0
			for n > 0 {
				prev := rows[n-1]
				prevCost := int(horizontal[prev*w+x])
				num := cost + y*y - prevCost - prev*prev
				den := 2 * (y - prev)
				start = num / den
				if num%den > 0 {
					start++
				}
				if start > starts[n-1] {
					break
				}
				n--
			}
			if n == 0 {
				start = 0
			}
			rows[n], starts[n] = y, start
			n++
		}
		k := 0
		for y := range h {
			if !ref[y*w+x] {
				continue
			}
			total++
			if n == 0 {
				continue
			}
			for k+1 < n && starts[k+1] <= y {
				k++
			}
			row := rows[k]
			d := y - row
			dist := int(horizontal[row*w+x]) + d*d
			if float64(dist) <= limit {
				matched++
			}
		}
	}
	return matched, total
}
