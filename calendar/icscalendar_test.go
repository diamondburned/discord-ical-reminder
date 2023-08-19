package calendar

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	_ "embed"

	"github.com/alecthomas/assert/v2"
)

//go:embed test.ics
var testICS string

//go:embed test_no_rrules.ics
var testNoRRulesICS string

var fixedTZ = time.FixedZone("America/Los_Angeles", -8*60*60)

// testICSNow is intentionally in November to be near DST.
var testICSNow = time.Date(2022, time.November, 1, 3, 3, 29, 125211746, fixedTZ)

func TestEventsWithin(t *testing.T) {
	now := testICSNow

	cal, err := ParseICS(strings.NewReader(testICS))
	assert.NoError(t, err)

	eventsExpect := cal.EventsBetween(now, now.Add(24*time.Hour), EventsOpts{})
	eventsGot := EventsWithin(cal, now, 24*time.Hour, EventsOpts{})

	assert.Equal(t, eventsExpect, eventsGot)
}

func TestICSCalendar(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	assert.NoError(t, err)

	expect := []Event{
		{
			StartsAt:    time.Date(2022, time.November, 1, 17, 00, 0, 0, losAngeles),
			EndsAt:      time.Date(2022, time.November, 1, 19, 50, 0, 0, losAngeles),
			Summary:     "GEOL 101L",
			Location:    "MH 203",
			Description: "",
			Status:      "CONFIRMED",
			Reminders:   []Reminder{},
		},
	}

	now := testICSNow

	cal, err := ParseICS(strings.NewReader(testICS))
	assert.NoError(t, err)

	events := cal.EventsBetween(now, now.Add(24*time.Hour), EventsOpts{})
	assert.Equal(t, expect, events)
}

func TestICSCalendar_recurrence(t *testing.T) {
	// This is the start of the day of the first event in test_no_rrules.ics.
	now := time.Date(2022, time.August, 30, 0, 0, 0, 0, fixedTZ)

	cal, err := ParseICS(strings.NewReader(testICS))
	assert.NoError(t, err)

	expectEvents := cal.EventsBetween(now, now.Add(1*Day), EventsOpts{})
	assert.Equal(t, 1, len(expectEvents))

	t.Run("this_week", func(t *testing.T) {
		events := cal.EventsBetween(now, now.Add(7*Day), EventsOpts{})
		assert.Equal(t, expectEvents, events)
	})

	t.Run("next_week", func(t *testing.T) {
		expectEvents := slices.Clone(expectEvents)
		expectEvents[0].StartsAt = expectEvents[0].StartsAt.Add(7 * Day)
		expectEvents[0].EndsAt = expectEvents[0].EndsAt.Add(7 * Day)

		events := cal.EventsBetween(now.Add(7*Day), now.Add(14*Day), EventsOpts{})
		assert.Equal(t, expectEvents, events)
	})

	t.Run("dst", func(t *testing.T) {
		losAngeles, err := time.LoadLocation("America/Los_Angeles")
		assert.NoError(t, err)

		now := testICSNow.In(losAngeles)

		events1 := cal.EventsBetween(now, now.Add(1*Day), EventsOpts{})
		assert.Equal(t, "2022-11-01 17:00:00 -0700 PDT", events1[0].StartsAt.String())

		events2 := cal.EventsBetween(now.Add(7*Day), now.Add(8*Day), EventsOpts{})
		// Ensure that the timezone is changed but the time is not.
		// Technically, the time changed, but the timezone change
		// should cancel it out.
		assert.Equal(t, "2022-11-08 17:00:00 -0800 PST", events2[0].StartsAt.String())
	})
}

func TestICSCalendar_nonRecurrence(t *testing.T) {
	// This is the start of the day of the first event in test_no_rrules.ics.
	now := time.Date(2022, time.August, 30, 0, 0, 0, 0, fixedTZ)

	cal, err := ParseICS(strings.NewReader(testNoRRulesICS))
	assert.NoError(t, err)

	t.Run("this_week", func(t *testing.T) {
		events := cal.EventsBetween(now, now.Add(7*Day), EventsOpts{})
		assert.Equal(t, 1, len(events))
	})

	t.Run("next_week", func(t *testing.T) {
		events := cal.EventsBetween(now.Add(7*Day), now.Add(14*Day), EventsOpts{})
		assert.Equal(t, 0, len(events))
	})
}

func TestICSCalendar_reminder(t *testing.T) {
	now := testICSNow

	cal, err := ParseICS(strings.NewReader(testICS))
	assert.NoError(t, err)

	opts := EventsOpts{
		IncludeReminders: true,
		ParseReminder: func(e Event) []Reminder {
			return []Reminder{
				{
					Action:   ReminderActionDisplay,
					RemindAt: e.StartsAt.Add(-10 * time.Minute),
				},
				{
					Action:   ReminderActionEmail,
					RemindAt: e.StartsAt.Add(-1 * Day),
				},
			}
		},
	}

	t.Run("today", func(t *testing.T) {
		// Inquire for today. There should be one event with two reminders.
		events := cal.EventsBetween(now, now.Add(1*Day), opts)
		assert.Equal(t, 1, len(events))
		assert.Equal(t, 2, len(events[0].Reminders))
	})

	t.Run("yesterday", func(t *testing.T) {
		// Inquire for yesterday. There should be the same output as for today.
		events := cal.EventsBetween(now.Add(-1*Day), now, opts)
		assert.Equal(t, 1, len(events))
		assert.Equal(t, 2, len(events[0].Reminders))
	})
}

var rruleRe = regexp.MustCompile(`(?m)^RRULE:.*\n`)

func icsRemoveRRules(ics string) string { return rruleRe.ReplaceAllString(ics, "") }
