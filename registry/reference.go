package registry

import (
	"fmt"
	"strings"
)

// Reference renders the registry as Markdown tables, one section per kind in
// table order. The output depends only on the table, so it can be checked in
// or compared against a golden file.
func Reference() string {
	var b strings.Builder
	ctxName := map[Context]string{CtxDocument: "document", CtxPart: "part item", CtxShape: "shape block", CtxCollision: "collision item"}
	for _, k := range kinds {
		fmt.Fprintf(&b, "## %s (%s)\n\n%s\n\n", k.Name, ctxName[k.Context], k.Doc)
		if len(k.Fields) > 0 {
			b.WriteString("| Field | Type | Default | Notes |\n|---|---|---|---|\n")
			for _, f := range k.Fields {
				def := "absent"
				switch {
				case f.Unsupported != "":
					def = "-"
				case f.Required:
					def = "required"
				case f.Default != nil:
					def = "`" + f.Default.Format() + "`"
				case f.Derived != "":
					def = f.Derived
				}
				notes := f.Doc
				if f.Unsupported != "" {
					notes = "Outside the first compiler's subset (" + f.Unsupported + ")."
				}
				if f.Runtime {
					notes += " May bind a runtime parameter."
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", f.Name, f.Type.Name(), def, strings.TrimSpace(notes))
			}
			b.WriteString("\n")
		}
		for _, r := range k.Rows {
			cols := make([]string, len(r.Columns))
			for i, c := range r.Columns {
				cols[i] = c.Name
			}
			fmt.Fprintf(&b, "Row `%s (%s)`, at least %d.\n\n", r.Name, strings.Join(cols, ", "), r.MinRows)
		}
		if len(k.Blocks) > 0 {
			fmt.Fprintf(&b, "Child blocks: %s.\n\n", strings.Join(k.Blocks, ", "))
		}
	}
	return b.String()
}
