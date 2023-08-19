package calendar

import (
	"cmp"
	"time"

	"github.com/emersion/go-ical"
)

// Event is a calendar event.
type Event struct {
	StartsAt    time.Time
	EndsAt      time.Time
	Summary     string // title
	Location    string
	Description string
	Status      EventStatus
	Reminders   []Reminder
}

// CompareEvent compares two events by start time.
func CompareEvent(a, b Event) int { return CompareTime(a.StartsAt, b.StartsAt) }

// CompareTime compares two times.
func CompareTime(a, b time.Time) int { return cmp.Compare(a.UnixNano(), b.UnixNano()) }

// timeIncludes returns true if t is between start and end.
func timeIncludes(start, end, t time.Time) bool {
	return t.After(start) && t.Before(end)
}

// Within returns true if the event starts within the given time range.
// If includeReminders is true, the event's reminders are also checked.
func (e Event) Within(start, end time.Time, includeReminders bool) bool {
	if timeIncludes(start, end, e.StartsAt) {
		return true
	}
	if includeReminders {
		for _, r := range e.Reminders {
			if timeIncludes(start, end, r.RemindAt) {
				return true
			}
		}
	}
	return false
}

// EventStatus is an event status.
type EventStatus = ical.EventStatus

const (
	EventStatusUnknown EventStatus = ""
	EventTentative     EventStatus = ical.EventTentative
	EventConfirmed     EventStatus = ical.EventConfirmed
	EventCancelled     EventStatus = ical.EventCancelled
)

// Reminder is a calendar event reminder.
type Reminder struct {
	Action   ReminderAction
	RemindAt time.Time
}

// ReminderTimes returns a list of trigger times for the given reminders.
func ReminderTimes(reminders []Reminder) []time.Time {
	return mapSlice(reminders, func(r Reminder) time.Time { return r.RemindAt })
}

// CalculateReminderTimes calculates reminder times for the given startsAt
// timestamp. Note that all durations are subtracted from the startsAt time.
func CalculateReminderTimes(startsAt time.Time, durations []time.Duration) []time.Time {
	return mapSlice(durations, func(d time.Duration) time.Time {
		return startsAt.Add(-d)
	})
}

// NewReminders creates a list of reminders from a list of times.
func NewReminders(times []time.Time, action ReminderAction) []Reminder {
	return mapSlice(times, func(t time.Time) Reminder {
		return Reminder{RemindAt: t, Action: action}
	})
}

// NewRemindersFromDuration creates a list of reminders from a list of
// durations.
func NewRemindersFromDuration(startsAt time.Time, durations []time.Duration, action ReminderAction) []Reminder {
	return NewReminders(CalculateReminderTimes(startsAt, durations), action)
}

// EarliestReminder returns the earliest reminder from a list of reminders.
func EarliestReminder(reminders []Reminder) Reminder {
	if len(reminders) == 0 {
		return Reminder{}
	}
	latest := reminders[0]
	for _, r := range reminders[1:] {
		if r.RemindAt.Before(latest.RemindAt) {
			latest = r
		}
	}
	return latest
}

// ReminderAction is a reminder action type. It is defined by the iCalendar
// specification.
type ReminderAction string

const (
	ReminderActionAudio   ReminderAction = "AUDIO"
	ReminderActionDisplay ReminderAction = "DISPLAY"
	ReminderActionEmail   ReminderAction = "EMAIL"
)

// Calendar describes a generic calendar. For a specific implementation, see
// ICSCalendar.
type Calendar interface {
	// EventsBetween returns events between start and end. The returned events
	// are sorted by start time. If includeReminders is true, then it will also
	// search for events that have reminders that fall within the time range.
	EventsBetween(start, end time.Time, opts EventsOpts) []Event
}

// EventsWithin returns events that are happening within the given duration from
// the given time.
func EventsWithin(c Calendar, t time.Time, d time.Duration, opts EventsOpts) []Event {
	return c.EventsBetween(t, t.Add(d), opts)
}

// EventsOpts are options for EventsBetween.
type EventsOpts struct {
	// ParseReminder, if not nil, is called for every event to extract reminders
	// from it.
	ParseReminder ReminderParseFunc
	// DefaultReminders is a list of default reminders to add to all events.
	// Durations in this list are subtracted from the event's start time.
	DefaultReminders []time.Duration
	// DefaultReminderAction is the default reminder action to use for all
	// default reminders. If empty, the action will be set to DISPLAY.
	DefaultReminderAction ReminderAction
	// IncludeReminders will also search for events that have reminders that
	// fall within the time range.
	IncludeReminders bool
	// ExcludeCancelled will exclude cancelled events.
	ExcludeCancelled bool
}

// EventReminders returns a list of reminders for the given event.
// It is a helper function that collects reminders from the reminder parser and
// the default reminders.
func (o EventsOpts) EventReminders(e Event) []Reminder {
	reminderAction := o.DefaultReminderAction
	if reminderAction == "" {
		reminderAction = ReminderActionDisplay
	}

	reminders := NewRemindersFromDuration(e.StartsAt, o.DefaultReminders, reminderAction)
	if o.ParseReminder != nil {
		reminders = append(reminders, o.ParseReminder(e)...)
	}

	return reminders
}

// ReminderParseFunc parses reminders from a given event.
// This exists because the iCalendar specification does not define a standard
// way to represent reminders. This function is called for every event to
// extract reminders from it.
type ReminderParseFunc func(Event) []Reminder

func mapSlice[T1, T2 any](slice []T1, f func(T1) T2) []T2 {
	mapped := make([]T2, len(slice))
	for i, v := range slice {
		mapped[i] = f(v)
	}
	return mapped
}
