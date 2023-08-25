package calendar

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"github.com/emersion/go-ical"
	"github.com/pkg/errors"
)

// ICSCalendar represents a calendar. An ICSCalendar is immutable: once created,
// it cannot be modified. It is safe to use from multiple goroutines.
type ICSCalendar struct {
	ical *ical.Calendar
}

var _ Calendar = (*ICSCalendar)(nil)

// NewICS creates a new calendar from an ICS calendar.
func NewICS(ical *ical.Calendar) *ICSCalendar {
	return &ICSCalendar{ical: ical}
}

// ParseICS parses an ICS-formatted calendar from r.
func ParseICS(r io.Reader) (*ICSCalendar, error) {
	dec := ical.NewDecoder(r)
	c, err := dec.Decode()
	if err != nil {
		return nil, err
	}
	return NewICS(c), nil
}

func (c *ICSCalendar) createEvent(src ical.Event, start, end time.Time, opts EventsOpts) Event {
	e := Event{
		StartsAt:    start,
		EndsAt:      end,
		Summary:     textProp(src.Props, ical.PropSummary),
		Location:    textProp(src.Props, ical.PropLocation),
		Description: textProp(src.Props, ical.PropDescription),
	}
	e.Status, _ = src.Status()
	e.Reminders = opts.EventReminders(e)
	return e
}

func textProp(props ical.Props, name string) string {
	prop := props.Get(name)
	if prop == nil {
		return ""
	}
	text, _ := prop.Text()
	return text
}

// Equals compares two calendars.
func (c *ICSCalendar) Equals(x *ICSCalendar) bool {
	if c == x {
		return true
	}
	if c == nil || x == nil {
		return false
	}

	var cBytes bytes.Buffer
	var xBytes bytes.Buffer

	err1 := ical.NewEncoder(&cBytes).Encode(c.ical)
	err2 := ical.NewEncoder(&xBytes).Encode(x.ical)
	if err1 != nil || err2 != nil {
		return false
	}

	return bytes.Equal(cBytes.Bytes(), xBytes.Bytes())
}

// EventsBetween implements Calendar.EventsBetween.
func (c *ICSCalendar) EventsBetween(start, end time.Time, opts EventsOpts) []Event {
	if start.Location() != end.Location() {
		panic("start and end must have the same location")
	}

	slog.Debug(
		"ics: searching events between %v and %v",
		"start", start,
		"end", end)

	location := start.Location()
	chosenEvents := make([]Event, 0, 8)

	for _, component := range c.ical.Children {
		if component.Name != ical.CompEvent {
			continue
		}

		icsEvent := ical.Event{Component: component}

		if opts.ExcludeCancelled {
			status, _ := icsEvent.Status()
			if status == EventCancelled {
				continue
			}
		}

		dtstart, err := icsEvent.DateTimeStart(location)
		if err != nil {
			continue
		}

		dtend, err := icsEvent.DateTimeEnd(location)
		if err != nil {
			continue
		}

		event := c.createEvent(icsEvent, dtstart, dtend, opts)

		// Prefer checking recurrence rules first.
		// Interesting blog: https://www.nylas.com/blog/calendar-events-rrules/.
		rrules, _ := icsEvent.RecurrenceSet(location)
		if rrules != nil {
			duration := dtend.Sub(dtstart)
			rstart := start
			rend := end

			// Expand the time range to search for reminders.
			if opts.IncludeReminders {
				// Get the earliest reminder of this event. We will use this as
				// the additional time range to search for reminders.
				latestReminder := EarliestReminder(event.Reminders)
				// Calculate for the duration between the start of the event and
				// the reminder time, since this reminder is only relevant for
				// the first occurrence of the event.
				latestReminderDuration := event.StartsAt.Sub(latestReminder.RemindAt)

				rend = rend.Add(latestReminderDuration)
			}

			// Copy the event for each relevant recurrence.
			for _, startsAt := range rrules.Between(rstart, rend, true) {
				event := c.createEvent(icsEvent, startsAt, startsAt.Add(duration), opts)
				chosenEvents = append(chosenEvents, event)
			}
		} else if event.Within(start, end, opts.IncludeReminders) {
			// No recurrence rules. Check if the event is in the range using
			// DTSTART and DTEND.
			chosenEvents = append(chosenEvents, event)
		}
	}

	slices.SortFunc(chosenEvents, CompareEvent)
	return chosenEvents
}

// OnlineICSCalendar represents an online calendar.
// It is safe for concurrent use.
type OnlineICSCalendar struct {
	// ICalURL is the URL to the ICS file.
	ICalURL string

	ical atomic.Pointer[ICSCalendar]
}

var _ Calendar = (*OnlineICSCalendar)(nil)

// NewOnlineICSCalendar creates a new online calendar tracking an ICS URL.
func NewOnlineICSCalendar(icalURL string) *OnlineICSCalendar {
	return &OnlineICSCalendar{ICalURL: icalURL}
}

// String implements fmt.Stringer.
func (c *OnlineICSCalendar) String() string {
	return fmt.Sprintf("OnlineICSCalendar(%v)", c.ICalURL)
}

// Refresh attempts to refresh the calendar. It returns an error if the calendar
// could not be updated. It returns true if the refreshed calendar is different
// from the previous calendar.
//
// Note that although this method is safe for concurrent use, it is not
// guaranteed that the calendar is not updated multiple times concurrently.
func (c *OnlineICSCalendar) Refresh(ctx context.Context) (changed bool, err error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ICalURL, nil)
	if err != nil {
		return false, errors.Wrap(err, "failed to create request")
	}

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("unexpected status: %v", resp.Status)
	}

	newCalendar, err := ParseICS(resp.Body)
	if err != nil {
		return false, errors.Wrap(err, "failed to parse calendar")
	}

	oldCalendar := c.ical.Load()
	if oldCalendar.Equals(newCalendar) {
		return false, nil
	}

	swapped := c.ical.CompareAndSwap(oldCalendar, newCalendar)
	if !swapped {
		return false, nil
	}

	return true, nil
}

// EventsBetween implements Calendar.EventsBetween. If Update has not been
// called, it will return an empty slice.
func (c *OnlineICSCalendar) EventsBetween(start, end time.Time, opts EventsOpts) []Event {
	ical := c.ical.Load()
	if ical == nil {
		return nil
	}
	return ical.EventsBetween(start, end, opts)
}
