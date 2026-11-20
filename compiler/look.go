package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/image"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// The look of an output (milestone M3): atlas textures painted on the CPU,
// the colour image a palette material shows in a stock viewer, and the PNG
// bytes of each. Everything here is baked once per output; the levels of the
// output share the images.

// bakedImage is one image of the output, painted and encoded.
type bakedImage struct {
	name string
	img  *image.RGBA8
	png  []byte
	// key is the SHA-256 of the dimensions and pixels: two images with equal
	// pixels are one decoded image.
	key string
	// pngSHA256 is the SHA-256 of the encoded bytes.
	pngSHA256 string
}

func newBaked(name string, img *image.RGBA8) (*bakedImage, error) {
	b, err := image.EncodePNG(img)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	fmt.Fprintf(h, "greenware.image/1 %d %d\n", img.W, img.H)
	h.Write(img.Pix)
	ps := sha256.Sum256(b)
	return &bakedImage{name: name, img: img, png: b, key: hex.EncodeToString(h.Sum(nil)), pngSHA256: hex.EncodeToString(ps[:])}, nil
}

// imageSet is the baked images of one output.
type imageSet struct {
	atlases map[string]*bakedImage // atlas name -> its painted image
	fallbk  map[string]*bakedImage // material name -> its base texture image
}

// ImageInfo describes one image of a file.
type ImageInfo struct {
	Name      string `json:"name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Bytes     int    `json:"png_bytes"`
	SHA256    string `json:"png_sha256"`
	PixelsKey string `json:"pixels_sha256"`
}

var slotField = regexp.MustCompile(`^slot([0-9]+)$`)

// matLook is how a material uses textures.
type matLook struct {
	// pbr is the node whose fields are written as the glTF material: the
	// material itself, or the fallback of a Selena material.
	pbr    *resolve.Node
	parent *resolve.Node // the Selena material, or nil
	base   *bakedImage   // the base texture image, or nil
	mask   *bakedImage   // a Selena material's palette mask, or nil
	maskAt *resolve.Atlas
}

// atlasImage paints an atlas once.
func (e *elab) atlasImage(a *resolve.Atlas) (*bakedImage, bool) {
	if e.imgs.atlases == nil {
		e.imgs.atlases = map[string]*bakedImage{}
	}
	if b, ok := e.imgs.atlases[a.Name]; ok {
		return b, true
	}
	img, err := image.Bake(a.Spec())
	if err != nil {
		e.report(diag.CodeDomain, a.Span, "the atlas `%s` could not be painted: %v", a.Name, err)
		return nil, false
	}
	b, err := newBaked(a.Name, img)
	if err != nil {
		e.report(diag.CodeDomain, a.Span, "the atlas `%s` could not be encoded: %v", a.Name, err)
		return nil, false
	}
	e.imgs.atlases[a.Name] = b
	return b, true
}

// materialLook resolves the textures a material uses: its base texture image
// and, for a Selena material, the palette mask its descriptor names.
func (e *elab) materialLook(node *resolve.Node) (*matLook, bool) {
	ml := &matLook{pbr: node}
	if node.Kind == "selena" {
		ml.parent = node
		for _, c := range node.Children {
			if c.Kind == "pbr" {
				ml.pbr = c
			}
		}
		// The mask is the atlas a descriptor field names; a Selena material
		// with a palette has exactly one in this subset.
		for _, f := range node.Fields {
			if f.Value.Kind != units.KRef {
				continue
			}
			a := e.r.Atlas(f.Value.Str)
			if a == nil {
				continue
			}
			if ml.maskAt != nil {
				e.unsupported(node.Name, "the selena material `%s` names two atlases (`%s` and `%s`); this compiler reads one palette mask per material", node.Name, ml.maskAt.Name, a.Name)
				return nil, false
			}
			ml.maskAt = a
			b, ok := e.atlasImage(a)
			if !ok {
				return nil, false
			}
			ml.mask = b
		}
	}
	bt, has := ml.pbr.Get("base_texture")
	if !has || !bt.Valid() {
		return ml, true
	}
	if b, ok := e.imgs.fallbk[node.Name]; ok {
		ml.base = b
		return ml, true
	}
	if atlas, painted := resolve.ParsePaintRef(bt.Str); painted {
		a := e.r.Atlas(atlas)
		if a == nil {
			e.unsupported(node.Name, "the atlas `%s` was not found", atlas)
			return nil, false
		}
		maskImg, ok := e.atlasImage(a)
		if !ok {
			return nil, false
		}
		if a.Mode == image.ModePalette {
			var cols [256]*[4]byte
			for _, s := range a.Slots {
				v, _ := ml.parent.Get(fmt.Sprintf("slot%d", s))
				c := [4]byte{v.Color.R, v.Color.G, v.Color.B, v.Color.A}
				cols[s] = &c
			}
			painted, err := image.PaintPalette(maskImg.img, &cols)
			if err != nil {
				e.report(diag.CodeMatParam, e.spanOf(node.Name), "%v", err)
				return nil, false
			}
			b, err := newBaked(resolve.PaintRef(atlas), painted)
			if err != nil {
				e.report(diag.CodeDomain, e.spanOf(node.Name), "%v", err)
				return nil, false
			}
			ml.base = b
		} else {
			ml.base = maskImg
		}
	} else {
		a := e.r.Atlas(bt.Str)
		if a == nil {
			e.unsupported(node.Name, "the texture `%s` was not found; this compiler reads atlas textures", bt.Str)
			return nil, false
		}
		b, ok := e.atlasImage(a)
		if !ok {
			return nil, false
		}
		ml.base = b
	}
	if e.imgs.fallbk == nil {
		e.imgs.fallbk = map[string]*bakedImage{}
	}
	e.imgs.fallbk[node.Name] = ml.base
	return ml, true
}

// samplerOf maps a material's `filter` and `wrap` to glTF sampler state
// (language reference 4.2): nearest is nearest/nearest, linear is
// linear/linear, trilinear is linear with linear-mipmap-linear.
func samplerOf(filter, wrap string) gltf.Sampler {
	s := gltf.Sampler{MagFilter: 9729, MinFilter: 9987, WrapS: 10497, WrapT: 10497}
	switch filter {
	case "nearest":
		s.MagFilter, s.MinFilter = 9728, 9728
	case "linear":
		s.MagFilter, s.MinFilter = 9729, 9729
	}
	switch wrap {
	case "clamp":
		s.WrapS, s.WrapT = 33071, 33071
	case "mirror":
		s.WrapS, s.WrapT = 33648, 33648
	}
	return s
}

// maskSampler is the sampler of a palette mask. It is data: a stock sampler
// must not blend slot bytes, so it reads the nearest texel. The shading in red
// is not filtered by it either; a palette-aware renderer owns that choice.
func maskSampler(wrap string) gltf.Sampler {
	s := samplerOf("nearest", wrap)
	return s
}

// ---- the documents' tables ----------------------------------------------------

// texTables collects the images, textures and samplers of one file.
type texTables struct {
	doc      *gltf.Document
	images   map[string]int // image key -> index
	textures map[string]int // image key + sampler -> index
	samplers map[gltf.Sampler]int
	decoded  int
	infos    []ImageInfo
}

func newTexTables(doc *gltf.Document) *texTables {
	return &texTables{doc: doc, images: map[string]int{}, textures: map[string]int{}, samplers: map[gltf.Sampler]int{}}
}

// use returns the texture index of an image read with a sampler, adding the
// image, the sampler and the texture on first use.
func (t *texTables) use(b *bakedImage, sm gltf.Sampler) int {
	ii, ok := t.images[b.key]
	if !ok {
		ii = len(t.doc.Images)
		t.images[b.key] = ii
		t.doc.Images = append(t.doc.Images, gltf.Image{Name: b.name, MimeType: "image/png", Data: b.png})
		t.decoded += b.img.DecodedBytes()
		t.infos = append(t.infos, ImageInfo{Name: b.name, Width: b.img.W, Height: b.img.H, Bytes: len(b.png), SHA256: b.pngSHA256, PixelsKey: b.key})
	}
	si, ok := t.samplers[sm]
	if !ok {
		si = len(t.doc.Samplers)
		t.samplers[sm] = si
		t.doc.Samplers = append(t.doc.Samplers, sm)
	}
	key := b.key + "/" + strconv.Itoa(si)
	ti, ok := t.textures[key]
	if !ok {
		ti = len(t.doc.Textures)
		t.textures[key] = ti
		t.doc.Textures = append(t.doc.Textures, gltf.Texture{Name: b.name, Sampler: gltf.Int(si), Source: gltf.Int(ii)})
	}
	return ti
}

// slotName sorts descriptor slot fields by their number.
func slotNumber(name string) (int, bool) {
	m := slotField.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// sortedSlots lists the slot fields of a Selena material by slot number.
func sortedSlots(n *resolve.Node) []resolve.Field {
	var out []resolve.Field
	for _, f := range n.Fields {
		if _, ok := slotNumber(f.Name); ok {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := slotNumber(out[i].Name)
		b, _ := slotNumber(out[j].Name)
		return a < b
	})
	return out
}

// selenaExtras is the descriptor record of a Selena material, written in the
// material's own extras. The Selena artifact is not available to this
// compiler, so the record is marked pending and the glTF core material is the
// pbr fallback; a palette-aware renderer reads the mask texture and the slot
// colours (and their runtime bindings in the asset's runtime table) from here.
func (e *elab) selenaExtras(m *matOut, baseTex, maskTex int) json.RawMessage {
	n := m.selena
	str := func(name string) string { v, _ := n.Get(name); return v.Str }
	params := map[string]any{}
	for _, f := range n.Fields {
		if f.Name == "source" || f.Name == "name" {
			continue
		}
		params[f.Name] = descriptorValue(f.Value)
	}
	rec := map[string]any{
		"format": "greenware.selena/1", "source": str("source"), "name": str("name"),
		"artifact": "pending", "params": params,
	}
	if a := m.look.maskAt; a != nil {
		pal := map[string]any{
			"grid":      []int{a.Layout.Cols, a.Layout.Rows},
			"cell_size": a.Layout.CellSize,
			"inset":     f32(det.Round32(a.Layout.Inset)),
			"mode":      "color",
		}
		if a.Mode == image.ModePalette {
			pal["mode"] = "palette"
		}
		var cells []any
		for _, c := range a.Cells { // in index order
			cell := map[string]any{"name": c.Name, "index": c.Index}
			if a.Mode == image.ModePalette {
				cell["slot"] = c.Slot
			}
			cells = append(cells, cell)
		}
		pal["cells"] = cells
		if maskTex >= 0 {
			pal["mask_texture"] = maskTex
		}
		if baseTex >= 0 {
			pal["fallback_texture"] = baseTex
		}
		rec["palette"] = pal
	}
	return mustJSON(map[string]any{"greenware": rec})
}

// descriptorValue writes a Selena descriptor value as JSON-ready data.
func descriptorValue(v units.Value) any {
	switch v.Kind {
	case units.KNumber:
		return f32(det.Round32(v.Num))
	case units.KSwitch:
		return v.Bool
	case units.KColor:
		if v.Color.A == 255 {
			return fmt.Sprintf("#%02x%02x%02x", v.Color.R, v.Color.G, v.Color.B)
		}
		return v.Color.Hex()
	case units.KString, units.KRef, units.KLabel:
		return v.Str
	}
	return v.Format()
}

// primaryAtlas is the atlas a material's UVs address: the palette mask of a
// Selena material, else the atlas of the base texture. The front end has
// already checked that every atlas of the material lays a named cell out the
// same way.
func (e *elab) primaryAtlas(mat string) *resolve.Atlas {
	var node *resolve.Node
	for _, m := range e.r.Materials {
		if m.Name == mat {
			node = m
		}
	}
	if node == nil {
		return nil
	}
	refAtlas := func(n *resolve.Node, only string) *resolve.Atlas {
		for _, f := range n.Fields {
			if f.Value.Kind != units.KRef || (only != "" && f.Name != only) {
				continue
			}
			name := f.Value.Str
			if a, painted := resolve.ParsePaintRef(name); painted {
				name = a
			}
			if at := e.r.Atlas(name); at != nil {
				return at
			}
		}
		return nil
	}
	if node.Kind == "selena" {
		if a := refAtlas(node, ""); a != nil {
			return a
		}
		for _, c := range node.Children {
			if a := refAtlas(c, "base_texture"); a != nil {
				return a
			}
		}
		return nil
	}
	return refAtlas(node, "base_texture")
}

// bakeLook paints every image the output's reachable materials use and
// returns the pixel digests of the distinct images, sorted. The digests join
// the meaning hash: a changed painter, atlas or default colour changes it.
func (e *elab) bakeLook() ([]string, bool) {
	reach := map[string]bool{}
	for _, m := range e.r.Look.Materials {
		reach[m] = true
	}
	set := map[string]bool{}
	for _, m := range e.r.Materials {
		if !reach[m.Name] {
			continue
		}
		ml, ok := e.materialLook(m)
		if !ok {
			return nil, false
		}
		if ml.base != nil {
			set[ml.base.key] = true
		}
		if ml.mask != nil {
			set[ml.mask.key] = true
		}
	}
	var keys []string
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, true
}
