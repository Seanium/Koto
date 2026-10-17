package engine

import (
	"crypto/rand"
	"sync"
	"time"
)

// CopyJobs holds only active preview/apply attempts. Nothing is written to disk. The UI
// releases a job when its dialog closes; bounded expiry also covers abandoned windows.
type CopyJobs struct {
	mu   sync.Mutex
	seed string
	jobs map[string]*copyJob
}
type copyJob struct {
	fingerprint string
	plan        *DeliveryPlan
	result      *CarryResult
	at          time.Time
	bytes       int64
}

func NewCopyJobs() *CopyJobs { return &CopyJobs{seed: rand.Text(), jobs: map[string]*copyJob{}} }
func (j *CopyJobs) get(id string) *copyJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	if v := j.jobs[id]; v != nil {
		if time.Since(v.at) > time.Hour {
			delete(j.jobs, id)
			return nil
		}
		cp := *v
		return &cp
	}
	return nil
}
func (j *CopyJobs) put(id string, v *copyJob) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	for k, x := range j.jobs {
		if now.Sub(x.at) > time.Hour {
			delete(j.jobs, k)
		}
	}
	v.at, v.bytes = now, planBytes(v.plan)
	j.jobs[id] = v
	var retained int64
	for _, job := range j.jobs {
		retained += job.bytes
	}
	for len(j.jobs) > 32 || len(j.jobs) > 1 && retained > 64<<20 {
		var oldest string
		var at time.Time
		for k, x := range j.jobs {
			if k == id {
				continue
			}
			if oldest == "" || x.at.Before(at) {
				oldest, at = k, x.at
			}
		}
		retained -= j.jobs[oldest].bytes
		delete(j.jobs, oldest)
	}
}
func (j *CopyJobs) finish(id string, r *CarryResult) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if v := j.jobs[id]; v != nil {
		// Completed retries need only the receipt, never another copy of the native transcript.
		plan, result := *r.Plan, *r
		plan.Content, plan.Records, plan.Extra = "", nil, nil
		result.Plan = &plan
		v.plan, v.result, v.bytes = &plan, &result, planBytes(&plan)
	}
}
func (j *CopyJobs) Forget(id string) { j.mu.Lock(); defer j.mu.Unlock(); delete(j.jobs, id) }

// Sweep reuses the existing index ticker; no cleanup goroutine stays alive on its own.
func (j *CopyJobs) Sweep() {
	j.mu.Lock()
	defer j.mu.Unlock()
	for id, job := range j.jobs {
		if time.Since(job.at) > time.Hour {
			delete(j.jobs, id)
		}
	}
}
func (j *CopyJobs) Clear() { j.mu.Lock(); defer j.mu.Unlock(); clear(j.jobs) }

func planBytes(p *DeliveryPlan) int64 {
	if p == nil {
		return 0
	}
	var size func(any) int64
	size = func(v any) int64 {
		switch x := v.(type) {
		case string:
			return int64(len(x))
		case []byte:
			return int64(len(x))
		case map[string]any:
			var n int64
			for k, v := range x {
				n += int64(len(k)) + size(v)
			}
			return n
		case []any:
			var n int64
			for _, v := range x {
				n += size(v)
			}
			return n
		}
		return 0
	}
	return int64(len(p.Content)) + int64(len(p.Records))*256 + size(p.Extra) + 1024
}
