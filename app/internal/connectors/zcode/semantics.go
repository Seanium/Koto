package zcode

import (
	"koto/internal/core"
	"sort"
)

func runtimeContext(info core.JsonObject) bool {
	s := core.Obj(info["semantics"])
	if core.S(s["providerVisibility"]) == "hidden" {
		return true
	}
	// Genuine model answers also have origin=agent_runtime. Only hidden assistant
	// entries are context; origin alone identifies synthetic *user* prompts.
	if core.S(info["role"]) == "assistant" {
		return false
	}
	if core.S(s["origin"]) == "real_user" {
		return false
	}
	if core.S(s["origin"]) == "system" || core.S(s["origin"]) == "agent_runtime" {
		return true
	}
	if core.S(s["kind"]) == "compact_summary" {
		return false
	}
	if core.S(info["visibility"]) == "model-only" {
		return true
	}
	return info["synthetic"] == true && core.S(info["source"]) != "workflow_launch"
}

func syntheticContextPart(info, part core.JsonObject) bool {
	return part["synthetic"] == true && core.S(core.Obj(info["semantics"])["origin"]) != "real_user" && core.S(info["source"]) != "workflow_launch"
}

func metadataPart(p core.JsonObject) bool {
	switch core.S(p["type"]) {
	case "step-start", "step-finish", "snapshot", "patch", "retry", "timeline", "agent":
		return true
	}
	return false
}

// Native recovery keeps the last tool record for each call (a retry can leave an
// earlier pending record), and uses declaration order when all tools have it.
func orderedToolParts(parts []core.JsonObject) (active, superseded []core.JsonObject) {
	last := map[string]int{}
	for i, p := range parts {
		if core.S(p["type"]) == "tool" {
			last[core.S(p["callID"])] = i
		}
	}
	tools := []core.JsonObject{}
	completeOrder := true
	for i, p := range parts {
		if core.S(p["type"]) != "tool" {
			active = append(active, p)
			continue
		}
		if last[core.S(p["callID"])] != i {
			superseded = append(superseded, p)
			continue
		}
		tools = append(tools, p)
		if _, ok := core.Num(p["declarationIndex"]); !ok {
			completeOrder = false
		}
	}
	if completeOrder {
		sort.SliceStable(tools, func(i, j int) bool {
			a, _ := core.Num(tools[i]["declarationIndex"])
			b, _ := core.Num(tools[j]["declarationIndex"])
			return a < b
		})
	}
	return append(active, tools...), superseded
}
