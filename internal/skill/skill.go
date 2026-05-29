// Package skill embeds a static agent playbook that teaches AI agents
// how to use the dreamer CLI efficiently. Agents run `dreamer --skill`
// once to learn the command surface, then use it without repeated --help calls.
package skill

import _ "embed"

//go:embed skill.md
var Playbook string
