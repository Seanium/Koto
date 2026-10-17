package koto

import (
	"koto/internal/core"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisNativeContentReadOnly(t *testing.T) {
	s := newSandbox(t)
	const other = "bbbbbbbb-2222-4333-8444-555555555555"
	first := ccLinear(CCA, 3, "")
	second := ccLinear(other, 3, "")
	// Independent native IDs and timestamps, identical first two turns.
	for i, r := range second {
		o := r.(core.O)
		old := core.S(getKey(o, "uuid"))
		o = setKey(o, "uuid", "other-"+old)
		if p := getKey(o, "parentUuid"); p != nil {
			o = setKey(o, "parentUuid", "other-"+core.S(p))
		}
		second[i] = setKey(o, "timestamp", "2026-10-17T00:00:00Z")
	}
	o := second[len(second)-1].(core.O)
	o = setKey(o, "message", core.O{{K: "role", V: "assistant"}, {K: "content", V: []any{ccText("different final reply")}}})
	second[len(second)-1] = o
	f1, f2 := filepath.Join(ccDir(s.Claude), CCA+".jsonl"), filepath.Join(ccDir(s.Claude), other+".jsonl")
	writeLines(t, f1, first)
	writeLines(t, f2, second)
	a := s.app(enable("claude-code", "pi"))
	api := newAPI(a)
	api.Reindex(ctx)
	before1, before2 := text(t, f1), text(t, f2)
	index := core.ToJSON(a.Store.Sessions())
	result, err := api.Analyze(ctx, []string{"claude-code:" + CCA, "claude-code:" + other})
	must(t, err)
	eq(t, len(result.Sessions), 2)
	eq(t, len(result.Nodes), 4)
	for _, n := range result.Nodes {
		if n.Turn < 3 {
			eq(t, len(n.Sessions), 2)
		} else {
			eq(t, len(n.Sessions), 1)
		}
	}
	eq(t, text(t, f1), before1)
	eq(t, text(t, f2), before2)
	eq(t, core.ToJSON(a.Store.Sessions()), index)
	_, err = api.Analyze(ctx, []string{"claude-code:" + CCA, "pi:other"})
	ok(t, err != nil)
	_, err = api.Analyze(ctx, []string{"claude-code:" + CCA, "claude-code:" + CCA})
	ok(t, err != nil)
}

func TestAnalysisHashesBeyondDisplayLimit(t *testing.T) {
	event := func(txt string) *core.Event {
		return &core.Event{Kind: "message", Actor: "assistant", Parts: []core.Part{{"type": "text", "text": txt}}}
	}
	prefix := strings.Repeat("长", 24001)
	a, _, _ := analysisContent([]*core.Event{event(prefix + "A")})
	b, _, _ := analysisContent([]*core.Event{event(prefix + "B")})
	ok(t, a != b, "full content is compared even outside the preview")
}

func TestAnalysisDisplayLabelsDoNotAnnotateNativeContent(t *testing.T) {
	events := []*core.Event{
		{Kind: "message", Actor: "user", Parts: []core.Part{{"type": "text", "text": "提问\r\n回复\n工具", "signature": "omit", "sealed": "omit"}}},
		{Kind: "message", Actor: "assistant", Parts: []core.Part{{"type": "tool_call", "callId": "native-id", "rawName": "提问", "args": map[string]any{"text": "结果"}}}},
		{Kind: "tool_result", Actor: "tool", Parts: []core.Part{{"type": "tool_result", "callId": "native-id", "outcome": "ok", "parts": []core.Part{
			{"type": "reasoning", "text": "思考\n上下文"},
			{"type": "text", "text": "结果（ok）"},
			{"type": "image", "name": "回复", "mediaType": "image/png", "blob": map[string]any{"sha256": "sha"}},
		}}}},
	}
	before := core.ToJSON(events)
	canonical, display, preview := analysisContentDisplay(events)
	eq(t, display.text, "提问\n提问\n回复\n工具\n\n回复\n调用 提问\n{\n  \"text\": \"结果\"\n}\n\n工具\n结果（ok）\n思考\n思考\n上下文\n结果（ok）\n附件 回复 image/png\nsha")
	eq(t, core.ToJSON(display.labels), core.ToJSON([]AnalysisLabel{
		{Line: 0, Kind: "user"}, {Line: 5, Kind: "assistant"}, {Line: 6, Kind: "call", Value: "提问"},
		{Line: 11, Kind: "tool"}, {Line: 12, Kind: "result", Value: "ok"}, {Line: 13, Kind: "reasoning"},
		{Line: 17, Kind: "attachment", Value: "回复 image/png"},
	}))
	eq(t, preview, "提问\n回复\n工具")
	// This is the existing language-independent comparison representation, including
	// signature removal, line-ending normalization and stable tool-call identity.
	eq(t, canonical, `[{"kind":"message","parts":[{"text":"提问\n回复\n工具","type":"text"}],"role":"user"},{"kind":"message","parts":[{"args":{"text":"结果"},"callId":"call1","rawName":"提问","type":"tool_call"}],"role":"assistant"},{"kind":"tool_result","parts":[{"callId":"call1","outcome":"ok","parts":[{"text":"思考\n上下文","type":"reasoning"},{"text":"结果（ok）","type":"text"},{"blob":{"sha256":"sha"},"mediaType":"image/png","name":"回复","type":"image"}],"type":"tool_result"}],"role":"tool"}]`)
	eq(t, core.ToJSON(events), before)
	for _, role := range []string{"system", "harness"} {
		eq(t, core.ToJSON(analysisRole(role).labels), core.ToJSON([]AnalysisLabel{{Kind: "context"}}))
	}
	eq(t, len(analysisRole("native-role").labels), 0)
	// A native multiline name remains literal and cannot change diff row counts.
	multiline := analysisPart(core.Part{"type": "tool_call", "rawName": "提问\n回复", "args": nil})
	eq(t, multiline.text, "调用 提问\n回复\nnull")
	eq(t, len(multiline.labels), 0)
}

func TestAnalysisDisplayLimitKeepsOnlyCompleteLabels(t *testing.T) {
	display := analysisJoin([]analysisDisplay{analysisRole("user"), {text: "body"}, analysisLabel("call", "调用 verylong", "verylong")}, "\n")
	limited, truncated := limitAnalysisDisplay(display, 11)
	ok(t, truncated)
	eq(t, limited.text, string([]rune(display.text)[:11])+"\n…（内容较长，请在会话中查看全文）")
	eq(t, core.ToJSON(limited.labels), core.ToJSON([]AnalysisLabel{{Line: 0, Kind: "user"}, {Line: 3, Kind: "truncated"}}))
	full, truncated := limitAnalysisDisplay(display, len([]rune(display.text)))
	ok(t, !truncated)
	eq(t, full.text, display.text)
	eq(t, core.ToJSON(full.labels), core.ToJSON(display.labels))
}

func TestCopySelectedMessagesAndRetry(t *testing.T) {
	s := newSandbox(t)
	file := filepath.Join(ccDir(s.Claude), CCA+".jsonl")
	writeLines(t, file, ccLinear(CCA, 4, ""))
	a := s.app(enable("claude-code", "pi"))
	api := newAPI(a)
	api.Reindex(ctx)
	d, err := api.Thread(ctx, "claude-code:"+CCA, 0, "")
	must(t, err)
	// Select turn 1 and only turn 3's answer, skipping turn 2 completely.
	ids := []string{d.Events[0].ID, d.Events[1].ID, d.Events[5].ID}
	req := CarryRequest{From: d.Head, To: "pi", EventIDs: ids, RequestID: "excerpt"}
	preview := req
	preview.DryRun = true
	plan, err := api.Carry(ctx, preview)
	must(t, err)
	eq(t, plan.Plan.Mode, "create")
	before := text(t, file)
	copied, err := api.Carry(ctx, req)
	must(t, err)
	ok(t, copied.Applied)
	eq(t, copied.Plan.Target.Session, plan.Plan.Target.Session)
	native, err := api.Thread(ctx, "pi:"+copied.Plan.Target.Session, 0, "")
	must(t, err)
	nativeText := core.ToJSON(native.Events)
	ok(t, !strings.Contains(nativeText, "x question 2") && !strings.Contains(nativeText, "x question 3"), "view must show only what was actually copied")
	body := text(t, copied.Plan.Target.Path)
	ok(t, strings.Contains(body, "x question 1") && strings.Contains(body, "x answer 3"))
	ok(t, !strings.Contains(body, "x question 2") && !strings.Contains(body, "x question 3") && !strings.Contains(body, "x answer 4"))
	// An Agent continues the new session before a network/UI retry: successful retry must not rewrite it.
	appendRaw(t, copied.Plan.Target.Path, userPi("continued after copy"))
	newBytes := text(t, copied.Plan.Target.Path)
	again, err := api.Carry(ctx, req)
	must(t, err)
	ok(t, again.Applied)
	eq(t, again.Plan.ID, copied.Plan.ID)
	eq(t, text(t, copied.Plan.Target.Path), newBytes)
	req.EventIDs = []string{d.Events[7].ID}
	_, err = api.Carry(ctx, req)
	ok(t, err != nil, "changed selection cannot reuse operation ID")
	req.RequestID = "wrong"
	req.EventIDs = []string{"foreign-event"}
	_, err = api.Carry(ctx, req)
	ok(t, err != nil)
	req.RequestID = "empty"
	req.EventIDs = []string{}
	_, err = api.Carry(ctx, req)
	ok(t, err != nil)
	eq(t, text(t, file), before)
	// Same-Agent copying at the latest node also creates a separate session.
	fresh, err := api.Carry(ctx, CarryRequest{From: d.Head, To: "claude-code"})
	must(t, err)
	ok(t, fresh.Applied)
	ok(t, fresh.Plan.Target.Session != CCA)
	eq(t, text(t, file), before)
}
