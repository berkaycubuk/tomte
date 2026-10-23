package tomte

// Cost limits. Nobody watches a run, so an agent stops itself: after a
// model call that takes a run past its limit, and before a run when the
// day's runs have spent the daily limit. The day's spending is read from
// the run log, so it holds across restarts and across `run` processes.

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrCostLimit  = errors.New("cost limit reached")
	ErrDailyLimit = errors.New("daily cost limit reached")
)

type price struct{ in, out float64 } // dollars per million tokens

func (p *price) cost(u Usage) float64 {
	if p == nil {
		return 0
	}
	return (float64(u.PromptTokens)*p.in + float64(u.CompletionTokens)*p.out) / 1e6
}

// Price is what the model costs, in dollars per million input and output
// tokens, as the provider lists it. Runs are priced only when it's given.
func Price(in, out float64) Option {
	return func(a *Agent) { a.price = &price{in, out} }
}

// MaxCost limits what the agent spends, in dollars, per run and per day
// (local time). Zero means no limit. It needs Price.
func MaxCost(perRun, perDay float64) Option {
	return func(a *Agent) { a.maxRun, a.maxDay = perRun, perDay }
}

// budget is one run's view of the limits.
type budget struct {
	a     *Agent
	spent float64 // by this run, also counted in a.inflight
}

func (a *Agent) startBudget(start time.Time) (*budget, error) {
	b := &budget{a: a}
	if a.maxDay == 0 {
		return b, nil
	}
	a.mu.Lock()
	a.logged = a.spentOn(start)
	today := a.logged + a.inflight
	a.mu.Unlock()
	if today >= a.maxDay {
		return nil, fmt.Errorf("%w: $%.2f of $%.2f today", ErrDailyLimit, today, a.maxDay)
	}
	return b, nil
}

// spend adds a model call to the run and says whether a limit is now
// reached.
func (b *budget) spend(res *Result, u Usage) error {
	res.Usage.add(u)
	c := b.a.price.cost(u)
	res.Cost += c
	b.spent += c
	b.a.mu.Lock()
	b.a.inflight += c
	today := b.a.logged + b.a.inflight
	b.a.mu.Unlock()
	switch {
	case b.a.maxRun > 0 && res.Cost >= b.a.maxRun:
		return fmt.Errorf("%w: $%.2f of $%.2f for this run", ErrCostLimit, res.Cost, b.a.maxRun)
	case b.a.maxDay > 0 && today >= b.a.maxDay:
		return fmt.Errorf("%w: $%.2f of $%.2f today", ErrDailyLimit, today, b.a.maxDay)
	}
	return nil
}
