// Field-beacon generates a modular prop from a compact gameplay recipe.
package main

import (
	"flag"
	"fmt"
	"os"

	"m31labs.dev/greenware/examples/field-beacon/beacon"
)

func main() {
	var recipe beacon.Recipe
	flag.Uint64Var(&recipe.Seed, "seed", 7, "deterministic appearance seed")
	flag.IntVar(&recipe.Tiers, "tiers", 3, "tiers of panels (1–5)")
	flag.IntVar(&recipe.Panels, "panels", 4, "panels per tier (2–6)")
	path := flag.String("out", "field-beacon.glb", "output GLB")
	flag.Parse()
	glb, err := beacon.Build(recipe)
	if err == nil {
		err = os.WriteFile(*path, glb, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s: %d bytes\n", *path, len(glb))
}
