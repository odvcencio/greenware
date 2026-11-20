package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const commandsDoc = "docs/reference/commands.md"

type docExample struct {
	line int
	args []string
	exit int
	want []string
}

var exitNote = regexp.MustCompile(`\s+# exit (\d+)$`)

// docExamples reads the `$ greenware ...` lines of the fenced blocks of the
// command reference. The lines that follow a command, up to the next command
// or the end of the block, are its expected standard output.
func docExamples(t *testing.T, text string) []docExample {
	t.Helper()
	var out []docExample
	inFence := false
	var cur *docExample
	flush := func() {
		if cur != nil {
			for len(cur.want) > 0 && cur.want[len(cur.want)-1] == "" {
				cur.want = cur.want[:len(cur.want)-1]
			}
			out = append(out, *cur)
			cur = nil
		}
	}
	for i, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(ln, "```") {
			flush()
			inFence = !inFence
			continue
		}
		if !inFence {
			continue
		}
		if strings.HasPrefix(ln, "$ ") {
			flush()
			cmd := strings.TrimPrefix(ln, "$ ")
			ex := docExample{line: i + 1}
			if m := exitNote.FindStringSubmatch(cmd); m != nil {
				ex.exit, _ = strconv.Atoi(m[1])
				cmd = strings.TrimSpace(exitNote.ReplaceAllString(cmd, ""))
			}
			fields := strings.Fields(cmd)
			if len(fields) == 0 || fields[0] != "greenware" {
				t.Fatalf("%s:%d: example does not start with greenware: %q", commandsDoc, i+1, ln)
			}
			ex.args = fields[1:]
			cur = &ex
			continue
		}
		if cur != nil {
			cur.want = append(cur.want, ln)
		}
	}
	flush()
	return out
}

// snapshot lists every file under dir with its bytes, to detect writes.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			m[p+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		m[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestCommandReferenceExamples runs every example of the command reference
// and compares its standard output and exit status with the text shown.
func TestCommandReferenceExamples(t *testing.T) {
	t.Chdir("../..")
	b, err := os.ReadFile(commandsDoc)
	if err != nil {
		t.Fatal(err)
	}
	examples := docExamples(t, string(b))
	if len(examples) < 14 {
		t.Fatalf("only %d examples found", len(examples))
	}
	before := snapshot(t, "testdata")
	used := map[string]bool{}
	for _, ex := range examples {
		used[ex.args[0]] = true
		name := strings.Join(ex.args, " ")
		t.Run(name, func(t *testing.T) {
			code, out, _ := exec(t, ex.args...)
			if code != ex.exit {
				t.Errorf("%s:%d: exit %d, the page says %d", commandsDoc, ex.line, code, ex.exit)
			}
			got := strings.Split(strings.TrimRight(out, "\n"), "\n")
			if out == "" {
				got = nil
			}
			want := ex.want
			if n := len(want); n > 0 && want[n-1] == "..." {
				want = want[:n-1]
				if len(got) < len(want) {
					got = nil
				} else {
					got = got[:len(want)]
				}
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("%s:%d: output differs from the page\n--- got\n%s\n--- page\n%s", commandsDoc, ex.line, strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
	after := snapshot(t, "testdata")
	for p, v := range after {
		if bv, ok := before[p]; !ok || bv != v {
			t.Errorf("an example wrote %s", p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			t.Errorf("an example removed %s", p)
		}
	}
	// Every authoring command has an example.
	var missing []string
	for name := range toolCommands {
		if !used[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("no example for: %v", missing)
	}
}

// TestCommandReferenceCoversUsage keeps the page and the usage text in step:
// each tool command has a section, and each flag of its usage line is named
// in that section.
func TestCommandReferenceCoversUsage(t *testing.T) {
	b, err := os.ReadFile("../../" + commandsDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	sections := map[string]string{}
	parts := strings.Split(doc, "\n## ")
	for _, p := range parts[1:] {
		head, body, _ := strings.Cut(p, "\n")
		sections[strings.TrimPrefix(head, "greenware ")] = body
	}
	flagRE := regexp.MustCompile(`--[a-z-]+`)
	for _, ln := range strings.Split(toolsUsage, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		name := strings.Fields(ln)[1]
		body, ok := sections[name]
		if !ok {
			t.Errorf("%s has no section", name)
			continue
		}
		if !strings.Contains(body, strings.TrimSpace(ln)) {
			t.Errorf("section %s does not show the usage line %q", name, ln)
		}
		for _, f := range flagRE.FindAllString(ln, -1) {
			if !strings.Contains(body, f) {
				t.Errorf("section %s does not mention %s", name, f)
			}
		}
	}
	if len(sections) == 0 {
		t.Fatal("no sections")
	}
}
