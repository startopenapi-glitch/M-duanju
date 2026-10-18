package worker

import (
	"context"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"drama-platform/internal/service"
)

type Runner struct {
	svc    *service.Service
	mu     sync.Mutex
	active map[string]int

	advancing atomic.Bool
	resuming  atomic.Bool
}

func New(svc *service.Service) *Runner { return &Runner{svc: svc, active: map[string]int{}} }
func (r *Runner) Run() {
	interval := envInt("WORKER_INTERVAL_SEC", 5)
	timeout := time.Duration(envInt("WORKER_STALE_SEC", 300)) * time.Second
	if err := r.svc.RecordWorkerHeartbeat(); err != nil {
		log.Printf("[worker] heartbeat: %v", err)
	}
	r.svc.RecoverStuckJobs()
	r.svc.ReclaimStaleJobs(timeout)
	r.svc.ResetStaleRuns(context.Background(), 15*time.Minute)
	r.orchestrate()
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if err := r.svc.RecordWorkerHeartbeat(); err != nil {
			log.Printf("[worker] heartbeat: %v", err)
		}
		r.svc.ReclaimStaleJobs(timeout)
		r.svc.ResetStaleRuns(context.Background(), 15*time.Minute)
		r.tick()
		r.orchestrate()
	}
}

// orchestrate 把「推进执行」和「消费续跑唤醒」放到独立 goroutine，
// 避免剧本生成等慢步骤阻塞 shot/assemble 的定时认领。
func (r *Runner) orchestrate() {
	if r.advancing.CompareAndSwap(false, true) {
		go func() {
			defer r.advancing.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			r.svc.AdvanceActiveRuns(ctx, 10)
		}()
	}
	if r.resuming.CompareAndSwap(false, true) {
		go func() {
			defer r.resuming.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if n, err := r.svc.ResumeFromWakeups(ctx, 10); err != nil {
				log.Printf("[worker] resume wakeups: %v", err)
			} else if n > 0 {
				log.Printf("[worker] resumed %d runs from recharge wakeups", n)
			}
		}()
	}
}

func (r *Runner) tick() {
	for kind, capacity := range map[string]int{service.JobKeyVisual: 1, service.JobShot: 3, service.JobAssemble: 1} {
		r.mu.Lock()
		free := capacity - r.active[kind]
		r.mu.Unlock()
		if free <= 0 {
			continue
		}
		jobs, err := r.svc.ClaimJobs(kind, free)
		if err != nil {
			log.Printf("[worker] claim: %v", err)
			continue
		}
		for _, j := range jobs {
			r.mu.Lock()
			r.active[kind]++
			r.mu.Unlock()
			go func(j *service.Job) {
				defer func() { r.mu.Lock(); r.active[j.Kind]--; r.mu.Unlock() }()
				if err := r.svc.ExecuteJob(j); err != nil {
					log.Printf("[worker] job=%s: %v", j.ID, err)
				}
			}(j)
		}
	}
	r.svc.PollAllGenerating()
}

func envInt(k string, def int) int {
	if n, e := strconv.Atoi(os.Getenv(k)); e == nil && n > 0 {
		return n
	}
	return def
}
