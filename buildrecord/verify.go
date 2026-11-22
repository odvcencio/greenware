// Package buildrecord verifies the artifact inventory Greenware publishes next to its GLBs.
package buildrecord

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const Schema = "greenware.build/1"
const VerificationSchema = "greenware.verify/1"

var ErrInvalidRecord = errors.New("invalid build record")

// Options controls checks beyond the files listed by the record.
type Options struct {
	// Strict also flags unlisted GLBs in the record's directory.
	Strict bool
}

// Report contains one result per listed file, in record order.
type Report struct {
	Schema        string   `json:"schema"`
	OK            bool     `json:"ok"`
	Files         []Check  `json:"files"`
	Unexpected    []string `json:"unexpected"`
	RecordChanged bool     `json:"record_changed"`
}

// Check describes the observed file and the values expected by the record.
type Check struct {
	Output         string `json:"output"`
	Level          int    `json:"level"`
	Name           string `json:"name"`
	ExpectedBytes  int64  `json:"expected_bytes"`
	ExpectedSHA256 string `json:"expected_sha256"`
	ActualBytes    *int64 `json:"actual_bytes,omitempty"`
	ActualSHA256   string `json:"actual_sha256,omitempty"`
	Status         string `json:"status"`
	Detail         string `json:"detail,omitempty"`
}

type inventory struct {
	Schema  string `json:"schema"`
	Outputs []struct {
		Name  string `json:"name"`
		Files []struct {
			Name   string `json:"name"`
			Level  *int   `json:"level"`
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	} `json:"outputs"`
}

// Verify checks a record against regular files beside it. Findings are in the
// report. A failure to open or parse the record, or to list a strict directory,
// returns an error. Records and artifacts are read through a directory root.
func Verify(recordPath string, options Options) (Report, error) {
	root, err := os.OpenRoot(filepath.Dir(recordPath))
	if err != nil {
		return Report{}, err
	}
	defer root.Close()
	name := filepath.Base(recordPath)
	file, before, err := openRegular(root, name)
	if err != nil {
		return Report{}, err
	}
	defer file.Close()
	report := Report{Schema: VerificationSchema, OK: true, Files: []Check{}, Unexpected: []string{}}
	listed, err := readInventory(file, &report)
	if err != nil {
		return Report{}, err
	}
	for i := range report.Files {
		checkFile(root, &report.Files[i])
		report.OK = report.OK && report.Files[i].Status == "ok"
	}
	if options.Strict {
		entries, err := fs.ReadDir(root.FS(), ".")
		if err != nil {
			return Report{}, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(name), ".glb") && !listed[name] {
				report.Unexpected = append(report.Unexpected, name)
			}
		}
		report.OK = report.OK && len(report.Unexpected) == 0
	}
	report.RecordChanged = !unchanged(root, name, before)
	report.OK = report.OK && !report.RecordChanged
	return report, nil
}

func readInventory(file io.Reader, report *Report) (map[string]bool, error) {
	decoder := json.NewDecoder(file)
	var record inventory
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%w: expected one JSON object", ErrInvalidRecord)
	}
	if record.Schema != Schema {
		return nil, fmt.Errorf("%w: expected schema %q, got %q", ErrInvalidRecord, Schema, record.Schema)
	}
	files := make(map[string]bool)
	outputs := make(map[string]bool)
	for _, output := range record.Outputs {
		if output.Name == "" || outputs[output.Name] || len(output.Files) == 0 {
			return nil, fmt.Errorf("%w: missing, empty or duplicate output %q", ErrInvalidRecord, output.Name)
		}
		outputs[output.Name] = true
		levels := make(map[int]bool)
		for _, file := range output.Files {
			if !filepath.IsLocal(file.Name) || strings.ContainsAny(file.Name, "/\\\\") ||
				strings.HasPrefix(file.Name, ".") || !strings.HasSuffix(file.Name, ".glb") {
				return nil, fmt.Errorf("%w: %q is not a GLB basename", ErrInvalidRecord, file.Name)
			}
			if files[file.Name] || file.Level == nil || levels[*file.Level] || *file.Level < 0 || file.Bytes <= 0 || !validDigest(file.SHA256) {
				return nil, fmt.Errorf("%w: invalid or duplicate listing for %q in output %q", ErrInvalidRecord, file.Name, output.Name)
			}
			files[file.Name] = true
			levels[*file.Level] = true
			report.Files = append(report.Files, Check{
				Output: output.Name, Level: *file.Level, Name: file.Name,
				ExpectedBytes: file.Bytes, ExpectedSHA256: file.SHA256,
			})
		}
	}
	if len(report.Files) == 0 {
		return nil, fmt.Errorf("%w: no GLBs listed", ErrInvalidRecord)
	}
	return files, nil
}

func validDigest(text string) bool {
	sum, err := hex.DecodeString(text)
	return err == nil && len(sum) == sha256.Size && text == hex.EncodeToString(sum)
}

func openRegular(root *os.Root, name string) (*os.File, fs.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%q is not a regular file", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err == nil && (!opened.Mode().IsRegular() || !os.SameFile(info, opened)) {
		err = fmt.Errorf("%q changed while opening", name)
	}
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, opened, nil
}

func unchanged(root *os.Root, name string, before fs.FileInfo) bool {
	now, err := root.Lstat(name)
	return err == nil && os.SameFile(before, now) && before.Size() == now.Size() && before.ModTime().Equal(now.ModTime())
}

func checkFile(root *os.Root, row *Check) {
	file, before, err := openRegular(root, row.Name)
	if err != nil {
		row.Status, row.Detail = "unreadable", err.Error()
		if errors.Is(err, fs.ErrNotExist) {
			row.Status, row.Detail = "missing", ""
		}
		return
	}
	defer file.Close()
	size := before.Size()
	row.ActualBytes = &size
	if size != row.ExpectedBytes {
		row.Status = "size_mismatch"
		return
	}
	digest := sha256.New()
	count, err := io.Copy(digest, io.LimitReader(file, size))
	if err != nil {
		row.Status, row.Detail = "unreadable", err.Error()
		return
	}
	if count != size || !unchanged(root, row.Name, before) {
		row.Status = "changed"
		return
	}
	row.ActualSHA256 = hex.EncodeToString(digest.Sum(nil))
	row.Status = "ok"
	if row.ActualSHA256 != row.ExpectedSHA256 {
		row.Status = "digest_mismatch"
	}
}
