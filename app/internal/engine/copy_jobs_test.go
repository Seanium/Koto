package engine

import (
	"strings"
	"testing"
	"time"
)

func TestCopyJobResources(t *testing.T) {
	j := NewCopyJobs()
	p := &DeliveryPlan{Content: strings.Repeat("x", 34<<20), Extra: map[string]any{"attachments": []any{map[string]any{"data": []byte("file")}}}}
	j.put("one", &copyJob{plan: p})
	j.put("two", &copyJob{plan: p})
	if len(j.jobs) != 1 || j.get("two") == nil {
		t.Fatal("preview byte budget must evict older plans")
	}
	j.finish("two", &CarryResult{Plan: p, Applied: true})
	job := j.get("two")
	if job.plan.Content != "" || job.plan.Extra != nil || job.result == nil || !job.result.Applied {
		t.Fatal("success retains only the receipt")
	}
	if p.Content == "" {
		t.Fatal("must not mutate a result still owned by its caller")
	}
	j.jobs["two"].at = time.Now().Add(-2 * time.Hour)
	j.Sweep()
	if len(j.jobs) != 0 {
		t.Fatal("idle expiry must release plans without a later copy")
	}
	for i := range 40 {
		j.put(string(rune(i)), &copyJob{plan: &DeliveryPlan{}})
	}
	if len(j.jobs) != 32 {
		t.Fatal("count limit")
	}
	j.Clear()
	if len(j.jobs) != 0 {
		t.Fatal("close releases all jobs")
	}
}
