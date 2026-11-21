// Command bakediff compares two athlete GLBs and names what changed: shells
// added, removed or resized; vertices that moved; UV, normal, skin and
// triangle changes; morph targets; clips; joints, bind pose and inverse bind
// matrices; materials, images and file size.
//
//	bakediff [-by shell|summary] [-json] [-scale-a k] [-scale-b k] [-record-a a.build.json] [-record-b b.build.json] before.glb after.glb
//
// The game's GLB carries no part names (one mesh node, one primitive), so
// without a build record its shells are the connected pieces of the mesh and
// are paired by order. A Greenware build record (-record-a/-record-b, the
// athlete.build.json next to the GLB) gives the census part names, and shells
// then pair by name. Vertices pair by index inside a shell when the vertex
// counts agree; otherwise by nearest position, and the report says so.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"m31labs.dev/greenware/compiler"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "bakediff:", err)
		os.Exit(1)
	}
}

func loadCensus(path string) ([]compiler.CensusPart, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var br struct {
		Census []struct{ Parts []compiler.CensusPart }
	}
	if err := json.Unmarshal(b, &br); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(br.Census) == 0 {
		return nil, fmt.Errorf("%s: no census", path)
	}
	return br.Census[0].Parts, nil
}

func run(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("bakediff", flag.ContinueOnError)
	by := fs.String("by", "shell", "detail: shell (every changed shell) or summary (totals and names only)")
	asJSON := fs.Bool("json", false, "write JSON")
	ra := fs.String("record-a", "", "build record of the before file (census part names)")
	sa := fs.Float64("scale-a", 1, "multiply every length in the before file by this (1/0.9144 turns metres into yards)")
	sb := fs.Float64("scale-b", 1, "multiply every length in the after file by this (1/0.9144 turns a Greenware build in metres into yards)")
	rb := fs.String("record-b", "", "build record of the after file (census part names)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 || (*by != "shell" && *by != "summary") {
		return fmt.Errorf("usage: bakediff [-by shell|summary] [-json] [-scale-a k] [-scale-b k] [-record-a f] [-record-b f] before.glb after.glb")
	}
	ca, err := loadCensus(*ra)
	if err != nil {
		return err
	}
	cb, err := loadCensus(*rb)
	if err != nil {
		return err
	}
	a, err := loadFile(fs.Arg(0), ca, *sa)
	if err != nil {
		return err
	}
	b, err := loadFile(fs.Arg(1), cb, *sb)
	if err != nil {
		return err
	}
	rep := compare(a, b)
	if *by == "summary" {
		rep.Shells.Changed = trim(rep.Shells.Changed)
	}
	if *asJSON {
		e := json.NewEncoder(w)
		e.SetIndent("", "  ")
		return e.Encode(rep)
	}
	text(w, rep, *by)
	return nil
}

// trim keeps only the names and counts of changed shells for the summary.
func trim(c []ShellDiff) []ShellDiff {
	out := make([]ShellDiff, len(c))
	for i, d := range c {
		d.Moved.Top = nil
		out[i] = d
	}
	return out
}

func list(w io.Writer, head string, xs []string) {
	if len(xs) == 0 {
		fmt.Fprintf(w, "%s: none\n", head)
		return
	}
	fmt.Fprintf(w, "%s (%d): %s\n", head, len(xs), strings.Join(xs, ", "))
}

func refs(rs []ShellRef) []string {
	var o []string
	for _, r := range rs {
		o = append(o, fmt.Sprintf("%s[%s %dv/%dt]", r.Name, r.Label, r.Vertices, r.Tris))
	}
	return o
}

func text(w io.Writer, r *Report, by string) {
	fi := func(tag string, f FileInfo) {
		fmt.Fprintf(w, "%s %s: %d bytes, %d vertices, %d triangles, %d shells, %d nodes, %d joints, %d clips, %d morph targets, %d materials, %d images\n",
			tag, f.Path, f.Bytes, f.Vertices, f.Triangles, f.Shells, f.Nodes, f.Joints, f.Clips, f.Targets, f.Materials, f.Images)
	}
	fi("before", r.Before)
	fi("after ", r.After)
	fmt.Fprintf(w, "shells before: %s\nshells after:  %s\n", r.Before.ShellMode, r.After.ShellMode)

	fmt.Fprintf(w, "\n== 1. shells ==\npairing: %s\n", r.Shells.Pairing)
	fmt.Fprintf(w, "matched %d: %d unchanged, %d changed\n", r.Shells.Matched, r.Shells.Unchanged, len(r.Shells.Changed))
	fmt.Fprintf(w, "shell order differs for %d of the matched shells\n", r.Shells.OutOfOrder)
	list(w, "added", refs(r.Shells.Added))
	list(w, "removed", refs(r.Shells.Removed))
	list(w, "resized", r.Shells.Resized)
	for _, d := range r.Shells.Changed {
		if d.Resized {
			fmt.Fprintf(w, "  resized %s[%s]: %dv/%dt -> %dv/%dt\n", d.Name, d.Label, d.VertsA, d.TrisA, d.VertsB, d.TrisB)
		}
	}

	fmt.Fprintf(w, "\n== 2. per shell: changed shells ==\n")
	var tm, tuv, tn, tj, tw, tt int
	for _, d := range r.Shells.Changed {
		tm += d.Moved.Count
		tuv += d.UV.Count
		tn += d.Normal.Count
		tj += d.JointSets
		tw += d.Weight.Count
		tt += d.Tris.Reordered + d.Tris.OnlyBefore + d.Tris.OnlyAfter
	}
	fmt.Fprintf(w, "totals: %d vertices moved (>%g), %d UV changed (>%g), %d normals changed (>%g), %d joint sets changed, %d weights changed (>%g), %d triangle index changes\n",
		tm, movedTol, tuv, uvTol, tn, normalTol, tj, tw, weightTol, tt)
	if by == "shell" {
		for _, d := range r.Shells.Changed {
			n := d.Name
			if d.NameAfter != "" {
				n += " -> " + d.NameAfter
			}
			fmt.Fprintf(w, "- %s [%s] %dv/%dt -> %dv/%dt, vertices matched by %s", n, d.Label, d.VertsA, d.TrisA, d.VertsB, d.TrisB, d.Match)
			if d.NoMatch > 0 {
				fmt.Fprintf(w, ", %d without a counterpart", d.NoMatch)
			}
			fmt.Fprintln(w)
			if d.Moved.Count > 0 {
				fmt.Fprintf(w, "    moved: %d, max %.6g, rms %.6g, bbox after (%.4f %.4f %.4f)..(%.4f %.4f %.4f), top vertices %v\n",
					d.Moved.Count, d.Moved.Max, d.Moved.RMS, d.Moved.Min[0], d.Moved.Min[1], d.Moved.Min[2], d.Moved.Maxp[0], d.Moved.Maxp[1], d.Moved.Maxp[2], d.Moved.Top)
			}
			if d.UV.Count > 0 {
				fmt.Fprintf(w, "    uv: %d changed, max %.6g\n", d.UV.Count, d.UV.Max)
			}
			if d.Normal.Count > 0 {
				fmt.Fprintf(w, "    normals: %d changed, max component diff %.4g\n", d.Normal.Count, d.Normal.Max)
			}
			if d.JointSets > 0 || d.Weight.Count > 0 {
				fmt.Fprintf(w, "    skin: %d joint sets differ, %d weights differ by >%g, max %.4g\n", d.JointSets, d.Weight.Count, weightTol, d.Weight.Max)
			}
			if d.Tris.Compared && (d.Tris.Reordered+d.Tris.OnlyBefore+d.Tris.OnlyAfter) > 0 {
				fmt.Fprintf(w, "    triangles: %d differ in list position or start corner, %d only before, %d only after (as vertex sets; a flipped diagonal shows as 1 and 1; 0 and 0 means the same triangles in another order)\n", d.Tris.Reordered, d.Tris.OnlyBefore, d.Tris.OnlyAfter)
			}
			if !d.Tris.Compared {
				fmt.Fprintf(w, "    triangles: not compared (vertex counts differ)\n")
			}
		}
	}

	fmt.Fprintf(w, "\n== 3. morph targets and clips ==\n")
	list(w, "targets added", r.Morph.Added)
	list(w, "targets removed", r.Morph.Removed)
	fmt.Fprintf(w, "targets changed %d, unchanged %d\n", len(r.Morph.Changed), r.Morph.Same)
	for _, t := range r.Morph.Changed {
		fmt.Fprintf(w, "- %s: max displacement %.5g -> %.5g, max per-vertex change %.5g, %d vertices, shells: %s\n", t.Name, t.MaxBefore, t.MaxAfter, t.MaxChange, t.Vertices, strings.Join(t.Shells, " "))
	}
	list(w, "clips added", r.Clips.Added)
	list(w, "clips removed", r.Clips.Removed)
	fmt.Fprintf(w, "clips changed %d, unchanged %d\n", len(r.Clips.Changed), r.Clips.Same)
	for _, c := range r.Clips.Changed {
		fmt.Fprintf(w, "- %s: keys %d -> %d, duration %.4g -> %.4g, %d channels changed (max diff %.5g; -1 = key counts differ)", c.Name, c.KeysBefore, c.KeysAfter, c.DurBefore, c.DurAfter, len(c.Changed), c.MaxDiff)
		if len(c.ChannelsAdded)+len(c.ChannelsRemoved) > 0 {
			fmt.Fprintf(w, ", channels +%d -%d", len(c.ChannelsAdded), len(c.ChannelsRemoved))
		}
		fmt.Fprintln(w)
		for _, n := range c.ChannelsRemoved {
			fmt.Fprintf(w, "    channel removed: %s\n", n)
		}
		for _, n := range c.ChannelsAdded {
			fmt.Fprintf(w, "    channel added: %s\n", n)
		}
		if by == "shell" {
			for i, ch := range c.Changed {
				if i == 6 {
					fmt.Fprintf(w, "    ... %d more\n", len(c.Changed)-6)
					break
				}
				fmt.Fprintf(w, "    %s %s: keys %d -> %d, max diff %.5g\n", ch.Node, ch.Path, ch.KeysBefore, ch.KeysAfter, ch.MaxDiff)
			}
		}
	}

	j := r.Joints
	fmt.Fprintf(w, "\n== 4. joints and nodes ==\n")
	fmt.Fprintf(w, "joints %d -> %d, order changed: %v\n", len(j.JointsBefore), len(j.JointsAfter), j.OrderChanged)
	list(w, "joints added", j.JointsAdded)
	list(w, "joints removed", j.JointsGone)
	list(w, "nodes added", j.NodesAdded)
	list(w, "nodes removed", j.NodesRemoved)
	fmt.Fprintf(w, "differences: %d\n", len(j.Diffs))
	for _, d := range j.Diffs {
		if d.Amount != 0 {
			fmt.Fprintf(w, "- %s: %s, max diff %.5g\n", d.Name, d.What, d.Amount)
		} else {
			fmt.Fprintf(w, "- %s: %s\n", d.Name, d.What)
		}
	}

	a := r.Assets
	fmt.Fprintf(w, "\n== 5. materials, images, size ==\n")
	fmt.Fprintf(w, "file bytes %d -> %d (%+d)\n", a.BytesBefore, a.BytesAfter, a.BytesAfter-a.BytesBefore)
	if a.Generator != "" {
		fmt.Fprintf(w, "generator %s\n", a.Generator)
	}
	fmt.Fprintf(w, "material differences: %d\n", len(a.Materials))
	for _, m := range a.Materials {
		fmt.Fprintf(w, "- %s: %s\n", m.Name, strings.Join(m.What, "; "))
	}
	fmt.Fprintf(w, "image differences: %d\n", len(a.Images))
	for _, s := range a.Images {
		fmt.Fprintf(w, "- %s\n", s)
	}
}
