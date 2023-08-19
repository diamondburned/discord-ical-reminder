package calendar

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"libdb.so/discord-ical-reminder/clocker"
)

// Day is a day duration.
const Day = 24 * time.Hour

// Notification is a calendar notification.
type Notification struct {
	// Calendar is the calendar that the event belongs to.
	Calendar Calendar
	// Event is the event that is about to happen.
	Event Event
	// RemindedAt is the time that the notification was sent.
	RemindedAt time.Time
}

// IsZero returns true if the notification is zero.
func (n Notification) IsZero() bool {
	return n.Calendar == nil
}

// NotifierState is the state of a Notifier.
type NotifierState struct {
	Calendars map[Calendar]struct{}
}

func newNotifierState() NotifierState {
	return NotifierState{
		Calendars: make(map[Calendar]struct{}),
	}
}

// AddCalendar adds a calendar to the notifier's state.
func (n *NotifierState) AddCalendar(cal Calendar) {
	n.Calendars[cal] = struct{}{}
}

// RemoveCalendar removes a calendar from the notifier's state.
func (n *NotifierState) RemoveCalendar(cal Calendar) {
	delete(n.Calendars, cal)
}

type NotifierOpts struct {
	EventsOpts
	// Location is the location of the calendar events. If nil, the
	// system's local time is used.
	Location *time.Location
	// SkipPastNotifications, if true, will skip notifications that were
	// supposed to be sent in the past for events that are still upcoming.
	// This is useful as a recovery mechanism if the notifier was down for
	// a while.
	SkipPastNotifications bool
}

// Notifier contains controls for a Monitor.
type Notifier struct {
	opts   NotifierOpts
	done   chan struct{}
	update chan struct{}

	mu    sync.Mutex
	state NotifierState
}

// NewNotifier creates a new notifier.
func NewNotifier(opts NotifierOpts) *Notifier {
	// Always include reminders.
	opts.IncludeReminders = true

	if opts.Location == nil {
		opts.Location = time.Local
	}

	return &Notifier{
		opts:   opts,
		done:   make(chan struct{}),
		update: make(chan struct{}, 1),
		state:  newNotifierState(),
	}
}

// Update updates the notifier's state.
// It runs fn synchronously, so it will block until the update is complete.
// It is safe to call Update from multiple goroutines.
func (n *Notifier) Update(fn func(*NotifierState)) {
	n.mu.Lock()
	fn(&n.state)
	n.mu.Unlock()
	n.Invalidate()
}

// Invalidate invalidates the notifier's state. It calls Update with a no-op
// function. Use this if you want to force the notifier to recompute its
// state when any of the calendars have changed.
func (n *Notifier) Invalidate() {
	select {
	case n.update <- struct{}{}:
	default:
	}
}

type notifyingEvent struct {
	Event
	Calendar Calendar
}

func (n *Notifier) notifyingEvents(start, end time.Time) []notifyingEvent {
	var notifyingEvents []notifyingEvent

	n.mu.Lock()
	defer n.mu.Unlock()

	for cal := range n.state.Calendars {
		slog.Debug("searching calendar", "calendar", cal)
		events := cal.EventsBetween(start, end, n.opts.EventsOpts)

		for _, ev := range events {
			slog.Debug("event found", "event", ev.Summary)
			if len(ev.Reminders) == 0 {
				continue
			}

			slices.SortFunc(ev.Reminders, func(a, b Reminder) int {
				return -1 * CompareTime(a.RemindAt, b.RemindAt)
			})

			notifyingEvents = append(notifyingEvents, notifyingEvent{
				Event:    ev,
				Calendar: cal,
			})
		}
	}

	slices.SortFunc(notifyingEvents, func(a, b notifyingEvent) int {
		return CompareEvent(a.Event, b.Event)
	})

	return notifyingEvents
}

func (n *Notifier) notifications(start, end time.Time) []Notification {
	events := n.notifyingEvents(start, end)
	notifications := make([]Notification, 0, len(events))

	for _, ev := range events {
		for _, reminder := range ev.Reminders {
			notifications = append(notifications, Notification{
				Calendar:   ev.Calendar,
				Event:      ev.Event,
				RemindedAt: reminder.RemindAt,
			})
		}
	}

	slices.SortFunc(notifications, func(a, b Notification) int {
		return CompareTime(a.RemindedAt, b.RemindedAt)
	})

	for _, notification := range notifications {
		slog.Debug("notification queued",
			"event", notification.Event.Summary,
			"reminded_at", notification.RemindedAt)
	}

	return notifications
}

// shouldSkip returns true if the notification should be skipped given the
// current time.
func (n *Notifier) shouldSkip(notification Notification, now time.Time) bool {
	startsAt := notification.Event.StartsAt
	if n.opts.SkipPastNotifications {
		// If we're skipping past notifications, then we should use the
		// notification's reminded at time instead of the event's start time.
		startsAt = notification.RemindedAt
	}
	return startsAt.Before(now)
}

// Notify starts the notifier. It returns when the context is done. It may drop
// notifications if the channel is not ready to receive them in time.
func (n *Notifier) Notify(ctx context.Context, dst chan<- Notification) error {
	select {
	case <-n.done:
		panic("notifier cannot be reused")
	default:
		defer close(n.done)
	}

	dayTicker := clocker.NewTicker(1 * Day)
	defer dayTicker.Stop()

	notificationTimer := (<-chan time.Time)(nil)
	notificationTimerStop := func() {}
	defer func() { notificationTimerStop() }()

	var notifications []Notification

	var refreshNotifications func(time.Time)
	var queueNext func(time.Time)

	refreshNotifications = func(now time.Time) {
		dayStart := dayStart(now)
		dayEnd := dayStart.Add(Day)

		slog.DebugContext(ctx,
			"refreshing notifications",
			"day_start", dayStart,
			"day_end", dayEnd)

		notifications = n.notifications(dayStart, dayEnd)
		queueNext(now)
	}

	queueNext = func(now time.Time) {
		// Ensure that the next tick is stopped before resetting it.
		notificationTimerStop()

		// Purge all late events. Don't actually skip past notifications for
		// future events, since we may have missed some notifications.
		for len(notifications) > 0 && n.shouldSkip(notifications[0], now) {
			notifications = notifications[1:]
		}

		if len(notifications) == 0 {
			slog.DebugContext(ctx,
				"no notifications queued, waiting for next day")
			return
		}

		next := notifications[0]
		slog.DebugContext(ctx,
			"next notification queued",
			"next_reminder", next.RemindedAt,
			"next_event", next.Event.Summary)

		t := time.NewTimer(next.RemindedAt.Sub(now))
		notificationTimer = t.C
		notificationTimerStop = func() { t.Stop() }
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case now := <-dayTicker.C:
			// Reset the events at the start of each day.
			slog.DebugContext(ctx,
				"day tick received, refreshing notifications")
			refreshNotifications(now.In(n.opts.Location))

		case <-n.update:
			slog.DebugContext(ctx,
				"calendar update received, refreshing notifications")
			refreshNotifications(time.Now().In(n.opts.Location))

		case now := <-notificationTimer:
			if len(notifications) == 0 {
				panic("timer fired but no notifications are queued")
			}

			// Next tick is used to wake up the loop when the next event is
			// about to happen.
			select {
			case <-ctx.Done():
				return ctx.Err()
			// case <-nextTickTimeout.C:
			// 	// Notification is no longer relevant.
			// 	slog.WarnContext(ctx,
			// 		"dropped notification",
			// 		"notification", nextNotification)
			case dst <- notifications[0]:
				// Explicitly remove the notification from the queue.
				// QueueNext won't do this for us until the event itself has
				// started, in case we missed some notifications.
				notifications = notifications[1:]
				queueNext(now.In(n.opts.Location))
			}
		}
	}
}

func drainTimer(t *time.Timer) {
	// https://stackoverflow.com/questions/55400661/go-timer-deadlock-on-stop
	// https://groups.google.com/g/golang-dev/c/c9UUfASVPoU/m/tlbK2BpFEwAJ
	if !t.Stop() {
		<-t.C
	}
}

// dayStart returns the start of the day for the given time. It uses the
// timezone configured in the given timestamp.
func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
