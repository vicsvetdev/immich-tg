package shutdown

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCaused(t *testing.T) {
	running := context.Background()
	stopped, stop := context.WithCancel(context.Background())
	stop()
	cancelled := fmt.Errorf("telegram sendVideo: %w", context.Canceled)
	genuine := errors.New("telegram sendVideo: 400 Bad Request: wrong file identifier")
	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"a cancellation once shutdown has begun", stopped, cancelled, true},
		{"a genuine failure once shutdown has begun", stopped, genuine, false},
		{"a timeout once shutdown has begun", stopped, fmt.Errorf("call: %w", context.DeadlineExceeded), false},
		{"a cancellation before shutdown", running, cancelled, false},
		{"no failure", stopped, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Caused(tt.ctx, tt.err); got != tt.want {
				t.Errorf("Caused = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOutliveIsNotCancelledWithItsParent(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	out, cancel := Outlive(parent)
	defer cancel()

	stop()
	select {
	case <-out.Done():
		t.Fatal("ended with its parent")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	<-out.Done()
	if cause := context.Cause(out); !errors.Is(cause, context.Canceled) {
		t.Errorf("cause after cancel = %v, want context.Canceled", cause)
	}
}
