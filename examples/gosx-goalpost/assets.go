package main

import (
	"fmt"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/lang"
)

var ribbonNames = [2]string{"ribbon_l", "ribbon_r"}

type goalpost struct {
	variant string
	lod     int
	model   []byte
	sockets [2]mesh.Vec3
}

func compileGoalposts(source []byte) ([]goalpost, error) {
	file, diagnostics := ast.Lower(lang.Parse(source))
	if diag.HasErrors(diagnostics) {
		return nil, fmt.Errorf("lower goal post: %v", diagnostics)
	}
	product, diagnostics := compiler.Build(file, "")
	if product == nil || diag.HasErrors(diagnostics) {
		return nil, fmt.Errorf("build goal post: %v", diagnostics)
	}
	var posts []goalpost
	for _, output := range product.Outputs {
		for _, level := range output.Files {
			doc, err := gltf.Decode(level.GLB)
			if err != nil {
				return nil, fmt.Errorf("decode %s: %w", level.Name, err)
			}
			sockets, err := findRibbons(doc)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", level.Name, err)
			}
			posts = append(posts, goalpost{output.Name, level.Level, level.GLB, sockets})
		}
	}
	return posts, nil
}

// This fixture uses translation-only socket paths. Fail if a future version
// rotates or scales one so the consumer cannot silently misplace its flag.
func findRibbons(doc *gltf.Document) ([2]mesh.Vec3, error) {
	var sockets [2]mesh.Vec3
	var seen [2]bool
	var walk func(int, mesh.Vec3, bool) error
	walk = func(index int, parent mesh.Vec3, translated bool) error {
		node := doc.Nodes[index]
		translated = translated && node.Rotation == nil && node.Scale == nil
		if p := node.Translation; p != nil {
			parent = mesh.Add(parent, mesh.Vec3{float64(p[0]), float64(p[1]), float64(p[2])})
		}
		for i, name := range ribbonNames {
			if node.Name != "socket."+name {
				continue
			}
			if seen[i] || !translated {
				return fmt.Errorf("socket %q is duplicated or has a non-translation frame", name)
			}
			sockets[i], seen[i] = parent, true
		}
		for _, child := range node.Children {
			if err := walk(child, parent, translated); err != nil {
				return err
			}
		}
		return nil
	}
	if doc.Scene == nil {
		return sockets, fmt.Errorf("goal post has no default scene")
	}
	for _, root := range doc.Scenes[*doc.Scene].Nodes {
		if err := walk(root, mesh.Vec3{}, true); err != nil {
			return sockets, err
		}
	}
	for i, ok := range seen {
		if !ok {
			return sockets, fmt.Errorf("missing socket %q", ribbonNames[i])
		}
	}
	return sockets, nil
}
