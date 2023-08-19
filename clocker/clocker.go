package clocker

import (
	"time"
)

// Ticker holds the channel that delivers ticks
type Ticker struct {
	C    <-chan time.Time
	done chan struct{}
}

// NewTicker returns a new ticker, similar to stdlib's time.Ticker
func NewTicker(d time.Duration) *Ticker {
	c := make(chan time.Time)
	t := &Ticker{
		C:    c,
		done: make(chan struct{}),
	}

	go func() {
		// Make a timer while rounding it to the next tick frame
		timer := time.NewTimer(getDurationForNextFrame(d))
		for {
			select {
			case <-t.done:
				timer.Stop()
				return
				// Hang until the timer ends, then send that over the channel
			case t := <-timer.C:
				// Either send the tick to the channel, or drop it if it
				// has not been consumed
				select {
				case c <- t:
				default:
				}
				// Reset the timer, loop restarts
				timer.Reset(getDurationForNextFrame(d))
			}
		}
	}()

	return t
}

// Stop stops the ticker
func (t *Ticker) Stop() {
	close(t.done)
}

// Tick is a shorthand for NewTicker(d).C.
func Tick(d time.Duration) <-chan time.Time {
	return NewTicker(d).C
}

func getDurationForNextFrame(frame time.Duration) time.Duration {
	tick := time.Now().Round(frame)
	if s := tick.Sub(time.Now()); s > 0 {
		return s
	}
	return tick.Add(frame).Sub(time.Now())
}
