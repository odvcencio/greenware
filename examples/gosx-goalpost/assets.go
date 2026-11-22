package main

import (
	"fmt"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/socket"
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
			frames, err := socket.ReadGLB(level.GLB)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", level.Name, err)
			}
			var sockets [2]mesh.Vec3
			for i, name := range ribbonNames {
				frame, err := frames.Find(name)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", level.Name, err)
				}
				if frame.Kind != "" && frame.Kind != "ribbon" {
					return nil, fmt.Errorf("%s: socket %q has kind %q, want ribbon", level.Name, name, frame.Kind)
				}
				sockets[i] = frame.Transform.T
			}
			posts = append(posts, goalpost{output.Name, level.Level, level.GLB, sockets})
		}
	}
	return posts, nil
}
