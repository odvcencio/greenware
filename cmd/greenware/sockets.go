package main

import (
	"fmt"
	"io"
	"os"

	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/socket"
)

const socketsUsage = "  greenware sockets <file.glb> [--json]\n"

type socketReport struct {
	Schema  string      `json:"schema"`
	Sockets []socketRow `json:"sockets"`
}

type socketRow struct {
	Name        string    `json:"name"`
	Kind        string    `json:"kind,omitempty"`
	Joint       string    `json:"joint,omitempty"`
	Node        int       `json:"node"`
	Translation mesh.Vec3 `json:"translation"`
	Linear      mesh.Mat3 `json:"linear"`
}

func runSockets(args []string, stdout, stderr io.Writer) int {
	o, err := parse(args, map[string]bool{"json": true})
	if err != nil {
		return usageFailure(stderr, "sockets", err, "usage:\n"+socketsUsage)
	}
	data, err := os.ReadFile(o.file)
	if err != nil {
		fmt.Fprintf(stderr, "greenware sockets: %v\n", err)
		return 2
	}
	sockets, err := socket.ReadGLB(data)
	if err != nil {
		fmt.Fprintf(stderr, "greenware sockets: %s: %v\n", o.file, err)
		return 1
	}
	if o.json {
		report := socketReport{Schema: "greenware.sockets/1", Sockets: make([]socketRow, 0, len(sockets))}
		for _, s := range sockets {
			report.Sockets = append(report.Sockets, socketRow{
				Name: s.Name, Kind: s.Kind, Joint: s.Joint, Node: s.Node,
				Translation: s.Transform.T, Linear: s.Transform.M,
			})
		}
		writeJSON(stdout, report)
		return 0
	}
	if len(sockets) == 0 {
		fmt.Fprintln(stdout, "No sockets in the initial scene.")
	}
	for _, s := range sockets {
		p := s.Transform.T
		fmt.Fprintf(stdout, "%s (node %d)", s.Name, s.Node)
		if s.Kind != "" {
			fmt.Fprintf(stdout, " kind=%s", s.Kind)
		}
		if s.Joint != "" {
			fmt.Fprintf(stdout, " joint=%s", s.Joint)
		}
		fmt.Fprintf(stdout, " at (%g, %g, %g) m\n", p[0], p[1], p[2])
	}
	return 0
}
