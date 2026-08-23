package pager

import (
	"strconv"
	"strings"
)

type slashCmd struct {
	Name string
	Help string
}

var slashCatalog = []slashCmd{
	{"help", "list commands"},
	{"goal", "<objective> | status | pause | resume | complete | clear"},
	{"login", "credentials live under ~/.opengrok"},
	{"model", "[id]  switch or show the current model"},
	{"quit", "leave Open Grok"},
	{"clear", "wipe the transcript view"},
	{"yolo", "toggle always-approve"},
	{"compact", "record a compaction checkpoint"},
	{"plan", "toggle plan mode (hard edit gate)"},
	{"sessions", "list sessions for this workspace"},
	{"export", "dump the transcript as markdown"},
	{"memory", "[query]  search project memory"},
	{"mcp", "list configured MCP servers"},
	{"status", "session / model / goal snapshot"},
	{"fork", "copy this conversation to a new session"},
	{"rewind", "drop the last exchange"},
}

func matchingSlash(prefix string) []slashCmd {
	p := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(prefix)), "/")
	if p == "" {
		return slashCatalog
	}
	var out []slashCmd
	for _, c := range slashCatalog {
		if strings.HasPrefix(c.Name, p) {
			out = append(out, c)
		}
	}
	return out
}

type goalVerb int

const (
	goalVerbStatus goalVerb = iota
	goalVerbPause
	goalVerbResume
	goalVerbComplete
	goalVerbClear
	goalVerbStart
)

type goalSlash struct {
	Verb      goalVerb
	Objective string
	Budget    *int64
}

func parseGoalSlash(arg string) goalSlash {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return goalSlash{Verb: goalVerbStatus}
	}
	switch strings.ToLower(fields[0]) {
	case "status":
		return goalSlash{Verb: goalVerbStatus}
	case "pause":
		return goalSlash{Verb: goalVerbPause}
	case "resume":
		return goalSlash{Verb: goalVerbResume}
	case "complete", "accept":
		return goalSlash{Verb: goalVerbComplete}
	case "clear":
		return goalSlash{Verb: goalVerbClear}
	}
	var budget *int64
	var obj []string
	for i := 0; i < len(fields); i++ {
		if fields[i] == "--budget" && i+1 < len(fields) {
			if n, err := strconv.ParseInt(fields[i+1], 10, 64); err == nil {
				budget = &n
			}
			i++
			continue
		}
		obj = append(obj, fields[i])
	}
	return goalSlash{Verb: goalVerbStart, Objective: strings.Join(obj, " "), Budget: budget}
}
