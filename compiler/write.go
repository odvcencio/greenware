package compiler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"m31labs.dev/greenware/datapkg"
)

// RecordSchema names the layout of the build record.
const RecordSchema = "greenware.build/1"

type recordFile struct {
	Name      string `json:"name"`
	Level     int    `json:"level"`
	Bytes     int    `json:"bytes"`
	SHA256    string `json:"sha256"`
	Triangles int    `json:"triangles"`
	Draws     int    `json:"draws"`
	Materials int    `json:"materials"`
	Vertices  int    `json:"vertices"`
	// The worst look and the stored counts, written only when a runtime branch
	// makes them differ from the default look.
	TrianglesWorst  int `json:"triangles_worst,omitempty"`
	TrianglesStored int `json:"triangles_stored,omitempty"`
	DrawsWorst      int `json:"draws_worst,omitempty"`
	Alternates      int `json:"alternates,omitempty"`
	// TextureBytes is the decoded RGBA size of the distinct images; Images
	// describes each. Both are absent for a file without images.
	TextureBytes int         `json:"texture_bytes,omitempty"`
	Images       []ImageInfo `json:"images,omitempty"`
	// The rig: joints, most influences on one vertex, morph targets,
	// animations and their keys. Absent for a file without a skeleton.
	Joints     int `json:"joints,omitempty"`
	Influences int `json:"influences,omitempty"`
	Morphs     int `json:"morphs,omitempty"`
	Animations int `json:"animations,omitempty"`
	AnimKeys   int `json:"animation_keys,omitempty"`
}

// LevelInfo is one level of an output in the build record and the build
// report: what the game needs to choose a file by distance. The fields are
// the same as the file's census row, plus the `lod` block's own numbers.
type LevelInfo struct {
	Level int    `json:"level"`
	File  string `json:"file"`
	// DistanceM is the `distance` of the level's `lod` block in metres. It is
	// null for level 0, which is the source and declares none.
	DistanceM *float64 `json:"distance_m"`
	// MaxErrorM is the `max_error` of the `lod` block in metres; 0 for level 0.
	MaxErrorM float64 `json:"max_error_m"`
	Triangles int     `json:"triangles"`
	Draws     int     `json:"draws"`
}

// Levels returns the output's levels, sorted by level.
func (o *OutputProduct) Levels() []LevelInfo {
	out := make([]LevelInfo, 0, len(o.Files))
	for _, f := range o.Files {
		out = append(out, LevelInfo{
			Level: f.Level, File: f.Name, DistanceM: f.Distance, MaxErrorM: f.MaxError,
			Triangles: f.Triangles, Draws: f.Draws,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Level < out[j].Level })
	return out
}

type recordOutput struct {
	Name      string       `json:"name"`
	FirSHA256 string       `json:"fir_sha256"`
	Files     []recordFile `json:"files"`
	// Levels lists every level with its declared switch distance, so a game
	// can read the distances here instead of repeating them.
	Levels []LevelInfo `json:"levels"`
}

type recordInputs struct {
	Data []datapkg.Input `json:"data"`
}

type record struct {
	Schema       string `json:"schema"`
	Generator    string `json:"generator"`
	SourceSHA256 string `json:"source_sha256"`
	InputSHA256  string `json:"input_sha256"`
	// Inputs lists what the build read besides the source: the data packages.
	// It is left out when there are none.
	Inputs  *recordInputs  `json:"inputs,omitempty"`
	Outputs []recordOutput `json:"outputs"`
	// Census holds one row of measured figures for every output and level.
	Census []CensusRow `json:"census"`
	// Audit holds the measurements of the rig audit, one entry per level.
	Audit       []*AuditReport `json:"audit,omitempty"`
	Unsupported []string       `json:"unsupported"`
	// Cost is the ticket receipt (CostVersion). It is in the record, not in
	// any GLB, so the files' bytes do not depend on the policy.
	Cost *Receipt `json:"cost,omitempty"`
}

// RecordName is the file name of the build record: the asset's name plus
// ".build.json".
func (p *Product) RecordName() string {
	name := p.Asset
	if name == "" {
		name = "greenware"
	}
	return name + ".build.json"
}

// Record returns the build record as JSON: the hash and measured facts of
// every file, in the order Write writes them. It holds no time, path or host
// detail, so the same build gives the same record anywhere.
func (p *Product) Record() []byte {
	rec := record{
		Schema: RecordSchema, Generator: Generator,
		SourceSHA256: p.SourceSHA256, InputSHA256: p.InputSHA256,
		Outputs: []recordOutput{}, Census: p.CensusRows(), Audit: p.Audits(), Unsupported: append([]string{}, p.Unsupported...),
		Cost: p.Receipt,
	}
	if len(p.Data) > 0 {
		rec.Inputs = &recordInputs{Data: p.Data}
	}
	for _, o := range p.Outputs {
		ro := recordOutput{Name: o.Name, FirSHA256: o.FirSHA256, Files: []recordFile{}, Levels: o.Levels()}
		for _, f := range o.Files {
			rf := recordFile{
				Name: f.Name, Level: f.Level, Bytes: f.Size, SHA256: f.SHA256,
				Triangles: f.Triangles, Draws: f.Draws, Materials: f.Materials, Vertices: f.Vertices,
			}
			if f.TrianglesWorst != f.Triangles || f.DrawsWorst != f.Draws || f.TrianglesStored != f.Triangles || f.Alternates > 0 {
				rf.TrianglesWorst, rf.TrianglesStored, rf.DrawsWorst, rf.Alternates = f.TrianglesWorst, f.TrianglesStored, f.DrawsWorst, f.Alternates
			}
			rf.TextureBytes, rf.Images = f.TextureBytes, f.Images
			rf.Joints, rf.Influences, rf.Morphs, rf.Animations, rf.AnimKeys = f.Joints, f.Influences, f.Morphs, f.Animations, f.AnimKeys
			ro.Files = append(ro.Files, rf)
		}
		rec.Outputs = append(rec.Outputs, ro)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rec); err != nil {
		panic("compiler: " + err.Error())
	}
	return buf.Bytes()
}

// The file operations Write uses. Tests replace them to simulate a failure.
var (
	renameFile = os.Rename
	removeFile = os.Remove
)

// Write puts the product's files in dir, creating it if needed. The order is
// fixed: every output in order, its levels in order, then the build record.
// Each file is written to a temporary name in dir and renamed into place, so
// a reader sees the old file or the new one, never half of one. The record
// comes last, and an old record is removed before the first file changes: an
// interrupted build leaves no record that claims a complete set.
func Write(p *Product, dir string) error {
	if p == nil {
		return errors.New("compiler: no product to write")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := removeFile(filepath.Join(dir, p.RecordName())); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			if err := writeAtomic(dir, f.Name, f.GLB); err != nil {
				return fmt.Errorf("writing %s: %w", f.Name, err)
			}
		}
	}
	if err := writeAtomic(dir, p.RecordName(), p.Record()); err != nil {
		return fmt.Errorf("writing %s: %w", p.RecordName(), err)
	}
	return nil
}

func writeAtomic(dir, name string, data []byte) (err error) {
	tmp, err := os.CreateTemp(dir, ".greenware-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			removeFile(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return renameFile(tmpName, filepath.Join(dir, name))
}
