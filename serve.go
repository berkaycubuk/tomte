package tomte

// Serving: the agent runs as a long-lived process (a systemd service) and
// sleeps until a schedule is due. Runs a schedule missed while the agent
// was down aren't made up, as in cron. A schedule whose last run is still
// going skips its turn rather than piling up runs.

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// shutdownGrace is how long runs in progress may finish after a stop
// signal before they're cancelled. systemd waits 90 s by default.
const shutdownGrace = 60 * time.Second

func (a *Agent) serve(ctx context.Context) error {
	if len(a.schedules) == 0 && len(a.commands) == 0 {
		return errors.New("nothing to serve: add a schedule with tomte.Cron or tomte.Job, or a command with tomte.Command")
	}
	// Runs get a context of their own, so a stop signal lets them finish.
	runCtx, cancelRuns := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRuns()
	var wg sync.WaitGroup
	if len(a.commands) > 0 {
		a.receive(ctx, runCtx, &wg)
	}
	if len(a.schedules) == 0 {
		<-ctx.Done()
		a.drain(&wg, cancelRuns)
		return nil
	}
	running := make([]bool, len(a.schedules))
	var mu sync.Mutex

	now := a.now()
	next := make([]time.Time, len(a.schedules))
	for i, s := range a.schedules {
		next[i] = s.next(now)
		log.Printf("%s: %q next at %s: %s", a.name, s.spec, next[i].Format(time.DateTime), cut(s.task, 60))
	}

	for {
		soonest := 0
		for i := range next {
			if next[i].Before(next[soonest]) {
				soonest = i
			}
		}
		// Waking at least every minute keeps to the wall clock when it
		// jumps, or when the machine sleeps, which timers don't count.
		timer := time.NewTimer(min(time.Until(next[soonest]), time.Minute))
		select {
		case <-ctx.Done():
			timer.Stop()
			a.drain(&wg, cancelRuns)
			return nil
		case <-timer.C:
		}

		now = a.now()
		for i, s := range a.schedules {
			if now.Before(next[i]) {
				continue
			}
			next[i] = s.next(now)
			mu.Lock()
			busy := running[i]
			running[i] = true
			mu.Unlock()
			if busy {
				log.Printf("%s: %q is due but its last run hasn't finished; skipping", a.name, s.spec)
				continue
			}
			wg.Go(func() {
				a.runOnce(runCtx, "cron", s.task)
				mu.Lock()
				running[i] = false
				mu.Unlock()
			})
		}
	}
}

func (a *Agent) drain(wg *sync.WaitGroup, cancel context.CancelFunc) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return
	case <-time.After(shutdownGrace):
		log.Printf("%s: runs still going %s after the stop signal; cancelling them", a.name, shutdownGrace)
		cancel()
		<-done
	}
}
