package main

import (
	"io"

	"m31labs.dev/greenware/tools"
)

// toolCommands are the authoring commands, implemented in package tools. Each
// takes the arguments after the command name and returns the exit status.
var toolCommands = map[string]func(args []string, stdout, stderr io.Writer) int{
	"fix":      tools.Fix,
	"diff":     tools.DiffCommand,
	"describe": tools.Describe,
	"ir":       tools.IR,
	"init":     tools.Init,
	"lock":     tools.Lock,
	"explain":  tools.Explain,
	"search":   tools.Search,
	"trace":    tools.Trace,
}

const toolsUsage = `  greenware fix <file> [--write] [--code GW-...] [--choose N] [--revision SHA256] [--project DIR] [--json]
  greenware diff <old.gw> <new.gw> [--fail-on breaking|change] [--project DIR] [--json]
  greenware describe <file> [--project DIR] [--json]
  greenware ir <file> [--output NAME] [--project DIR] [--json]
  greenware init <dir> [--name NAME] [--author TEXT] [--license SPDX] [--dry-run] [--json]
  greenware lock [<dir>] [--check] [--dry-run] [--json]
  greenware explain <GW-CODE> [--json]
  greenware search <query> [dir] [--kind KIND] [--json]
  greenware trace <file> <name> [--project DIR] [--json]
`
