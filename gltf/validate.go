package gltf

import "fmt"

// Validate checks the references, sizes and numbers in d. Encode calls it, and
// Decode calls it on the document it builds, so a decoded document always
// encodes.
func (d *Document) Validate() error {
	idx := func(path string, v, n int) error {
		if v < 0 || v >= n {
			return errf(path, "index %d out of range (have %d)", v, n)
		}
		return nil
	}
	opt := func(path string, v *int, n int) error {
		if v == nil {
			return nil
		}
		return idx(path, *v, n)
	}
	if err := opt("scene", d.Scene, len(d.Scenes)); err != nil {
		return err
	}
	for i, s := range d.Scenes {
		for j, n := range s.Nodes {
			if err := idx(fmt.Sprintf("scenes[%d].nodes[%d]", i, j), n, len(d.Nodes)); err != nil {
				return err
			}
		}
	}
	parent := make([]int, len(d.Nodes))
	for i := range parent {
		parent[i] = -1
	}
	for i, n := range d.Nodes {
		p := fmt.Sprintf("nodes[%d]", i)
		if err := opt(p+".mesh", n.Mesh, len(d.Meshes)); err != nil {
			return err
		}
		if err := opt(p+".skin", n.Skin, len(d.Skins)); err != nil {
			return err
		}
		if n.Skin != nil && n.Mesh == nil {
			return errf(p+".skin", "a node with a skin needs a mesh")
		}
		for j, c := range n.Children {
			cp := fmt.Sprintf("%s.children[%d]", p, j)
			if err := idx(cp, c, len(d.Nodes)); err != nil {
				return err
			}
			if parent[c] >= 0 {
				return errf(cp, "node %d already has parent %d", c, parent[c])
			}
			parent[c] = i
		}
		if n.Translation != nil {
			if err := finite(p+".translation", n.Translation[:]); err != nil {
				return err
			}
		}
		if n.Rotation != nil {
			if err := finite(p+".rotation", n.Rotation[:]); err != nil {
				return err
			}
		}
		if n.Scale != nil {
			if err := finite(p+".scale", n.Scale[:]); err != nil {
				return err
			}
		}
	}
	// Each node has at most one parent, so a cycle is a parent chain that
	// returns to a node on the current walk.
	state := make([]uint8, len(d.Nodes)) // 0 new, 1 on this walk, 2 done
	for s := range d.Nodes {
		var walk []int
		for n := s; n >= 0 && state[n] != 2; n = parent[n] {
			if state[n] == 1 {
				return errf(fmt.Sprintf("nodes[%d].children", n), "node tree has a cycle")
			}
			state[n] = 1
			walk = append(walk, n)
		}
		for _, n := range walk {
			state[n] = 2
		}
	}
	for i, m := range d.Meshes {
		for j, pr := range m.Primitives {
			p := fmt.Sprintf("meshes[%d].primitives[%d]", i, j)
			if len(pr.Positions) == 0 || len(pr.Positions)%3 != 0 {
				return errf(p+".attributes.POSITION", "need a positive multiple of 3 floats, have %d", len(pr.Positions))
			}
			nv := len(pr.Positions) / 3
			if err := finite(p+".attributes.POSITION", pr.Positions); err != nil {
				return err
			}
			if len(pr.Normals) != 0 {
				if len(pr.Normals) != 3*nv {
					return errf(p+".attributes.NORMAL", "have %d floats, need %d", len(pr.Normals), 3*nv)
				}
				if err := finite(p+".attributes.NORMAL", pr.Normals); err != nil {
					return err
				}
			}
			if len(pr.UV0) != 0 {
				if len(pr.UV0) != 2*nv {
					return errf(p+".attributes.TEXCOORD_0", "have %d floats, need %d", len(pr.UV0), 2*nv)
				}
				if err := finite(p+".attributes.TEXCOORD_0", pr.UV0); err != nil {
					return err
				}
			}
			if len(pr.Indices)%3 != 0 {
				return errf(p+".indices", "triangle list needs a multiple of 3 indices, have %d", len(pr.Indices))
			}
			if len(pr.Indices) == 0 && nv%3 != 0 {
				return errf(p+".attributes.POSITION", "non-indexed triangle list needs a multiple of 3 vertices, have %d", nv)
			}
			for k, v := range pr.Indices {
				if int64(v) >= int64(nv) {
					return errf(p+".indices", "index %d (value %d) is not below vertex count %d", k, v, nv)
				}
			}
			if err := opt(p+".material", pr.Material, len(d.Materials)); err != nil {
				return err
			}
			if err := validateSkinAttrs(p, pr, nv); err != nil {
				return err
			}
			if err := validateTargets(p, pr, nv); err != nil {
				return err
			}
			if len(pr.Targets) != len(m.Primitives[0].Targets) {
				return errf(p+".targets", "have %d targets; primitive 0 has %d", len(pr.Targets), len(m.Primitives[0].Targets))
			}
		}
		if err := validateMeshWeights(fmt.Sprintf("meshes[%d]", i), m); err != nil {
			return err
		}
	}
	if err := d.validateRig(); err != nil {
		return err
	}
	for i, m := range d.Materials {
		p := fmt.Sprintf("materials[%d]", i)
		if err := finite(p+".pbrMetallicRoughness.baseColorFactor", m.BaseColor[:]); err != nil {
			return err
		}
		if err := finite(p+".pbrMetallicRoughness", []float32{m.Metallic, m.Roughness}); err != nil {
			return err
		}
		if err := opt(p+".pbrMetallicRoughness.baseColorTexture.index", m.BaseColorTexture, len(d.Textures)); err != nil {
			return err
		}
		if m.BaseColorTexCoord < 0 {
			return errf(p+".pbrMetallicRoughness.baseColorTexture.texCoord", "negative texCoord %d", m.BaseColorTexCoord)
		}
		if err := finite(p+".emissiveFactor", m.EmissiveFactor[:]); err != nil {
			return err
		}
		for k, v := range m.EmissiveFactor {
			if v < 0 {
				return errf(fmt.Sprintf("%s.emissiveFactor[%d]", p, k), "value %v is negative", v)
			}
		}
		switch m.AlphaMode {
		case "", AlphaOpaque, AlphaBlend:
			if m.AlphaCutoff != 0 {
				return errf(p+".alphaCutoff", "set only with alphaMode MASK")
			}
		case AlphaMask:
			if !finite32(m.AlphaCutoff) || m.AlphaCutoff < 0 {
				return errf(p+".alphaCutoff", "value %v must be finite and not negative", m.AlphaCutoff)
			}
		default:
			return errf(p+".alphaMode", "%q is not OPAQUE, MASK or BLEND", m.AlphaMode)
		}
	}
	for i, t := range d.Textures {
		p := fmt.Sprintf("textures[%d]", i)
		if err := opt(p+".sampler", t.Sampler, len(d.Samplers)); err != nil {
			return err
		}
		if err := opt(p+".source", t.Source, len(d.Images)); err != nil {
			return err
		}
	}
	for i, im := range d.Images {
		p := fmt.Sprintf("images[%d]", i)
		switch {
		case len(im.Data) > 0 && im.URI != "":
			return errf(p, "set Data or URI, not both")
		case len(im.Data) == 0 && im.URI == "":
			return errf(p, "set Data or URI")
		case len(im.Data) > 0 && im.MimeType == "":
			return errf(p+".mimeType", "embedded image needs a mimeType")
		}
	}
	for i, s := range d.Samplers {
		for _, v := range []int{s.MagFilter, s.MinFilter, s.WrapS, s.WrapT} {
			if v < 0 {
				return errf(fmt.Sprintf("samplers[%d]", i), "negative enum value %d", v)
			}
		}
	}
	return nil
}

func finite(path string, fs []float32) error {
	for i, f := range fs {
		if !finite32(f) {
			return errf(fmt.Sprintf("%s[%d]", path, i), "value is %v; NaN and infinity are not allowed", f)
		}
	}
	return nil
}
