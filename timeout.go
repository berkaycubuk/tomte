package tomte

// Time limits. A tool or job that hangs, say on an HTTP call with no
// timeout, would otherwise keep its run going forever, and its schedule
// would skip every turn after. A run that reaches its limit stops
// waiting, even for code that doesn't watch its context: that code is
// left to finish on its own, and the run ends with ErrTimeLimit.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrTimeLimit is returned when a run, job or command takes longer than
// the agent's Timeout.
var ErrTimeLimit = errors.New("time limit reached")

// Timeout limits how long one run, job or command may take; it's 10
// minutes if not given, and 0 means no limit.
func Timeout(d time.Duration) Option {
	return func(a *Agent) { a.timeout = d }
}

// limit is ctx with the agent's time limit, which ends it with an error
// wrapping ErrTimeLimit.
func (a *Agent) limit(ctx context.Context) (context.Context, context.CancelFunc) {
	if a.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, a.timeout, fmt.Errorf("%w: %s", ErrTimeLimit, a.timeout))
}

// within runs fn and returns what it returns, or ctx's cause as soon as
// ctx is done, without waiting for fn.
func within[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := fn()
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, context.Cause(ctx)
	}
}
