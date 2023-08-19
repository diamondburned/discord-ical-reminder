package calendar

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestNotifier(t *testing.T) {
	notifier := NewNotifier(NotifierOpts{
		EventsOpts: EventsOpts{
			DefaultReminders: []time.Duration{0},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	notifications := make(chan Notification)
	go func() {
		if err := notifier.Notify(ctx, notifications); err != nil {
			t.Error(err)
		}
	}()

	notifier.Update(func(state *NotifierState) {
		// Allow some leeway since the notifier might not have started yet.
		now := time.Now().Add(150 * time.Millisecond)

		calendar := newMockCalendar([]Event{
			{
				StartsAt: now.Add(300 * time.Millisecond),
				EndsAt:   now.Add(450 * time.Millisecond),
				Reminders: []Reminder{
					// Before now.
					{RemindAt: now.Add(-150 * time.Millisecond)},
					// After now.
					{RemindAt: now.Add(150 * time.Millisecond)},
				},
			},
			{
				StartsAt: now.Add(500*time.Millisecond + 1*Day),
				EndsAt:   now.Add(500*time.Millisecond + 2*Day),
				Reminders: []Reminder{
					// Remind a day before the event.
					{RemindAt: now.Add(500 * time.Millisecond)},
				},
			},
		})

		state.AddCalendar(calendar)
	})

	expectDurations := []time.Duration{
		// Event 1
		450 * time.Millisecond, // explicit in event
		150 * time.Millisecond, // explicit in event
		0 * time.Millisecond,   // default from opts
		// Event 2
		1 * Day,
	}

	for i := 0; i < len(expectDurations); i++ {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for notification %d", i)
		case notification := <-notifications:
			remindedBefore := notification.Event.StartsAt.Sub(notification.RemindedAt)
			if remindedBefore != expectDurations[i] {
				t.Errorf(
					"notification %d: expected reminded before %v, got %v",
					i, expectDurations[i], remindedBefore)
			}
		}
	}
}

type mockCalendar struct {
	mu     sync.Mutex
	events []Event
}

func newMockCalendar(events []Event) *mockCalendar {
	return &mockCalendar{events: events}
}

func (c *mockCalendar) addEvents(events []Event) {
	c.mu.Lock()
	c.events = append(c.events, events...)
	c.mu.Unlock()
}

func (c *mockCalendar) EventsBetween(start, end time.Time, opts EventsOpts) []Event {
	var events []Event
	for _, e := range c.events {
		e.Reminders = append(e.Reminders, opts.EventReminders(e)...)
		if e.Within(start, end, opts.IncludeReminders) {
			events = append(events, e)
		}
	}

	slices.SortFunc(events, CompareEvent)
	return events
}
