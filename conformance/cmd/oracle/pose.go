package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"m31labs.dev/greenware/conformance/glb"
	"m31labs.dev/greenware/conformance/measure"
)

// PoseReport is the JSON value `oracle pose` prints.
type PoseReport struct {
	Validator string          `json:"validator"`
	File      string          `json:"file"`
	Clip      string          `json:"clip"` // empty for the rest pose
	Time      float64         `json:"time"`
	Findings  []glb.Finding   `json:"findings"`
	Bounds    *measure.Box    `json:"bounds"`
	Lowest    *measure.Lowest `json:"lowest_vertex"`
	Joints    []JointPosition `json:"joints"`
	Distances []DistanceEntry `json:"distances,omitempty"`
}

// JointPosition is a node's world position in the pose.
type JointPosition struct {
	Node     int        `json:"node"`
	Name     string     `json:"name"`
	Position [3]float64 `json:"position"`
}

// DistanceEntry is the distance between two named nodes in the pose.
type DistanceEntry struct {
	A        string  `json:"a"`
	B        string  `json:"b"`
	Distance float64 `json:"distance"`
}

const poseUsage = "usage: oracle pose file.glb [--clip NAME] [--time T] [--node NAME]... [--distance A:B]...\n"

func runPose(args []string, stdout, stderr io.Writer) int {
	var file, clip string
	var tm float64
	var nodes, dists []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		take := func(name string) (string, bool) {
			if v, ok := strings.CutPrefix(a, name+"="); ok {
				return v, true
			}
			if a == name && i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		var v string
		var ok bool
		switch {
		case a == "--clip" || strings.HasPrefix(a, "--clip="):
			if clip, ok = take("--clip"); !ok {
				fmt.Fprint(stderr, poseUsage)
				return 2
			}
		case a == "--time" || strings.HasPrefix(a, "--time="):
			if v, ok = take("--time"); !ok {
				fmt.Fprint(stderr, poseUsage)
				return 2
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				fmt.Fprintf(stderr, "oracle: --time %q: %v\n", v, err)
				return 2
			}
			tm = f
		case a == "--node" || strings.HasPrefix(a, "--node="):
			if v, ok = take("--node"); !ok {
				fmt.Fprint(stderr, poseUsage)
				return 2
			}
			nodes = append(nodes, v)
		case a == "--distance" || strings.HasPrefix(a, "--distance="):
			if v, ok = take("--distance"); !ok {
				fmt.Fprint(stderr, poseUsage)
				return 2
			}
			dists = append(dists, v)
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "oracle: unknown flag %s\n%s", a, poseUsage)
			return 2
		case file == "":
			file = a
		default:
			fmt.Fprint(stderr, poseUsage)
			return 2
		}
	}
	if file == "" {
		fmt.Fprint(stderr, poseUsage)
		return 2
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "oracle: %v\n", err)
		return 2
	}
	rep := &PoseReport{Validator: glb.Label, File: file, Clip: clip, Time: tm, Joints: []JointPosition{}}
	f, fs := glb.Decode(data, glb.Options{Resolve: resolver(filepath.Dir(file))})
	rep.Findings = fs
	if rep.Findings == nil {
		rep.Findings = []glb.Finding{}
	}
	if glb.HasErrors(fs) {
		// An invalid file is reported, not posed.
		if err := writeJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "oracle: %v\n", err)
			return 2
		}
		return 1
	}
	m, err := measure.Load(f)
	if err != nil {
		fmt.Fprintf(stderr, "oracle: %v\n", err)
		return 2
	}
	ci := -1
	if clip != "" {
		var ok bool
		if ci, ok = m.ClipByName(clip); !ok {
			fmt.Fprintf(stderr, "oracle: no animation named %q\n", clip)
			return 2
		}
	}
	p, err := m.PoseAt(ci, tm)
	if err != nil {
		fmt.Fprintf(stderr, "oracle: %v\n", err)
		return 2
	}
	if b, ok, err := m.PosedBounds(p, measure.Whole); err != nil {
		fmt.Fprintf(stderr, "oracle: %v\n", err)
		return 2
	} else if ok {
		rep.Bounds = &b
	}
	if lo, ok, err := m.LowestVertex(p, measure.Whole); err != nil {
		fmt.Fprintf(stderr, "oracle: %v\n", err)
		return 2
	} else if ok {
		rep.Lowest = &lo
	}
	add := func(i int) {
		rep.Joints = append(rep.Joints, JointPosition{Node: i, Name: m.NodeName(i), Position: p.Position(i)})
	}
	if len(nodes) == 0 {
		for _, i := range m.JointNodes() {
			add(i)
		}
	}
	for _, n := range nodes {
		i, ok := m.NodeID(n)
		if !ok {
			fmt.Fprintf(stderr, "oracle: no node named %q\n", n)
			return 2
		}
		add(i)
	}
	for _, d := range dists {
		a, b, ok := strings.Cut(d, ":")
		if !ok {
			fmt.Fprintf(stderr, "oracle: --distance %q: want A:B\n", d)
			return 2
		}
		dist, err := m.JointDistance(p, a, b)
		if err != nil {
			fmt.Fprintf(stderr, "oracle: %v\n", err)
			return 2
		}
		rep.Distances = append(rep.Distances, DistanceEntry{a, b, dist})
	}
	if err := writeJSON(stdout, rep); err != nil {
		fmt.Fprintf(stderr, "oracle: %v\n", err)
		return 2
	}
	return 0
}
