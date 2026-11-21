package main

import "m31labs.dev/greenware/gltf"

// scaleDoc multiplies every length in the document by k: positions, morph
// deltas, node translations, the translation part of inverse bind matrices and
// translation animation values. Greenware writes metres and the game's bake
// writes yards, so a build is compared with k = 1/0.9144.
func scaleDoc(d *gltf.Document, k float32) {
	if k == 1 {
		return
	}
	for mi := range d.Meshes {
		for pi := range d.Meshes[mi].Primitives {
			p := &d.Meshes[mi].Primitives[pi]
			for i := range p.Positions {
				p.Positions[i] *= k
			}
			for ti := range p.Targets {
				for i := range p.Targets[ti].Positions {
					p.Targets[ti].Positions[i] *= k
				}
			}
		}
	}
	for i := range d.Nodes {
		if t := d.Nodes[i].Translation; t != nil {
			for j := range t {
				t[j] *= k
			}
		}
	}
	for si := range d.Skins {
		for mi := range d.Skins[si].InverseBindMatrices {
			m := &d.Skins[si].InverseBindMatrices[mi]
			m[12] *= k
			m[13] *= k
			m[14] *= k
		}
	}
	for ai := range d.Animations {
		a := &d.Animations[ai]
		done := map[int]bool{}
		for _, c := range a.Channels {
			if c.Path != gltf.PathTranslation || done[c.Sampler] {
				continue
			}
			done[c.Sampler] = true
			for i := range a.Samplers[c.Sampler].Output {
				a.Samplers[c.Sampler].Output[i] *= k
			}
		}
	}
}
