// Command athleteparity builds a Greenware athlete and prints the game's
// ledger and silhouette numbers for it, optionally against the numbers of the
// game's Go-baked athlete.
//
//	go run ./internal/athleteparity/cmd/athleteparity [flags]
//
// Run it from the repository (the defaults are relative to the directory with
// go.mod). The output is text in the layout of the game's `-ledger` and
// `-silhouette -`; -compare prints the difference table and exits 1 on FAIL.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ap "m31labs.dev/greenware/internal/athleteparity"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("athleteparity", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	src := fs.String("src", "", "the athlete source (default examples/athlete/athlete.gw)")
	root := fs.String("root", "", "project root for the source's imports (default: nearest greenware.mod, else the repository's examples)")
	output := fs.String("output", "match", "the output to measure; the first output when the source has none of that name")
	looks := fs.String("looks", "", "the looks file (default examples/athlete/testdata/looks.json)")
	clip := fs.String("clip", "ready", "the clip to pose")
	at := fs.Float64("t", 0, "the time in the clip, in seconds")
	helmet := fs.Float64("helmet-yd", ap.DefaultHelmetYards, "helmet width in yards to use when the model has no helmet shell")
	alias := fs.String("alias", "", "shape=shell pairs, comma separated, to map shape names the tables do not know")
	gatePx := fs.Float64("gate-px", 96, "picture height of the silhouette gate, in pixels")
	noGate := fs.Bool("no-gate", false, "skip the pixel gate")
	compare := fs.String("compare", "", "a text file with the Go tools' printed ledger and silhouette; print the difference and a verdict")
	tolLedger := fs.Float64("tol-ledger", ap.DefaultTolerances.LedgerPercent, "with -compare: a ledger row's allowed difference, in percent")
	tolSil := fs.Float64("tol-sil", ap.DefaultTolerances.SilHelmets, "with -compare: a silhouette column's allowed difference, in helmet widths")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, err := ap.FindRepoRoot(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "athleteparity:", err)
		return 2
	}
	rel := func(p, def string) string {
		if p == "" {
			p = def
		}
		if filepath.IsAbs(p) {
			return p
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
		return filepath.Join(repo, p)
	}
	aliases := map[string]string{}
	for _, kv := range strings.Split(*alias, ",") {
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "athleteparity: -alias %q is not shape=shell\n", kv)
			return 2
		}
		aliases[k] = v
	}
	lf, err := ap.LoadLooks(rel(*looks, "examples/athlete/testdata/looks.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "athleteparity:", err)
		return 2
	}
	b, err := ap.Build(ap.BuildOptions{Source: rel(*src, "examples/athlete/athlete.gw"), Root: *root, RepoRoot: repo, Output: *output, Aliases: aliases})
	if err != nil {
		fmt.Fprintln(os.Stderr, "athleteparity:", err)
		return 1
	}
	rep := b.Model.Run(lf, ap.Options{Clip: *clip, T: *at, HelmetYards: *helmet, GatePx: *gatePx, NoGate: *noGate})
	fmt.Printf("athleteparity: output %s, file %s, looks from game commit %s, clip %s at t=%g\n\n", b.Output, b.File, lf.GameCommit, *clip, *at)
	rep.Write(os.Stdout)
	if *compare != "" {
		f, err := os.Open(rel(*compare, ""))
		if err != nil {
			fmt.Fprintln(os.Stderr, "athleteparity:", err)
			return 2
		}
		defer f.Close()
		base, err := ap.ParseBaseline(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "athleteparity:", err)
			return 2
		}
		fmt.Println()
		if !ap.Compare(os.Stdout, rep, base, ap.Tolerances{LedgerPercent: *tolLedger, SilHelmets: *tolSil}) {
			return 1
		}
	}
	return 0
}
