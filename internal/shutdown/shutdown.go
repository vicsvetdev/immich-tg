// Package shutdown tells failures caused by the service's shutdown from
// genuine ones, and lets the outcome of a genuine failure still be published
// once shutdown has begun.
package shutdown

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Grace bounds each call that still goes out once shutdown has begun: the
// outcome of a genuine failure, then the stopped message. Together they stay
// within the stop_grace_period Docker is given before it kills the process.
const Grace = 5 * time.Second

// errGraceOver ends a context from Outlive.
var errGraceOver = fmt.Errorf("the %s shutdown grace is over: %w", Grace, context.DeadlineExceeded)

// Caused reports whether err was caused by the shutdown: ctx, the service's
// context, is done and err is a cancellation. Any other failure is genuine,
// even one that happens once shutdown has begun.
func Caused(ctx context.Context, err error) bool {
	return ctx.Err() != nil && errors.Is(err, context.Canceled)
}

// Outlive returns a context that is not cancelled with ctx, the service's
// context, but ends Grace after ctx is done, counted from the call if ctx is
// already done by then, or at once when cancel is called. Its cause is then
// that the grace is over.
func Outlive(ctx context.Context) (context.Context, context.CancelFunc) {
	out, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-time.After(Grace):
				cancel(errGraceOver)
			case <-out.Done():
			}
		case <-out.Done():
		}
	}()
	return out, func() { cancel(context.Canceled) }
}
