package main

import (
	"encoding/json"
	"os"
	"time"

	"github.com/pkg/errors"
)

type config struct {
	Timezone           timezoneValue    `json:"timezone"`
	Calendars          []calendarConfig `json:"calendars"`
	RefreshFrequency   durationValue    `json:"refresh_frequency"`
	EventNotifications []durationValue  `json:"event_notifications"`
}

type calendarConfig struct {
	ICalURL         string `json:"ical_url"`
	WebhookURL      string `json:"webhook_url"`
	MessageTemplate string `json:"message_template"`
}

func parseConfigFiles(paths []string) (*config, error) {
	var cfg config
	for _, path := range paths {
		if err := parseConfigFile(path, &cfg); err != nil {
			return nil, errors.Wrapf(err, "failed to parse config file %s", path)
		}
	}
	return &cfg, nil
}

func parseConfigFile(path string, dst *config) error {
	f, err := os.Open(path)
	if err != nil {
		return errors.Wrap(err, "failed to open config file")
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(dst); err != nil {
		return errors.Wrap(err, "failed to decode config")
	}

	return nil
}

type durationValue time.Duration

func durationValues(durs []durationValue) []time.Duration {
	out := make([]time.Duration, 0, len(durs))
	for _, d := range durs {
		out = append(out, time.Duration(d))
	}
	return out
}

func (d durationValue) Duration() time.Duration {
	return time.Duration(d)
}

func (d *durationValue) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.Wrap(err, "failed to decode duration")
	}

	dur, err := time.ParseDuration(s)
	if err != nil {
		return errors.Wrap(err, "failed to parse duration")
	}

	*d = durationValue(dur)
	return nil
}

type timezoneValue time.Location

func (t *timezoneValue) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.Wrap(err, "failed to decode timezone")
	}

	loc, err := time.LoadLocation(s)
	if err != nil {
		return errors.Wrap(err, "failed to load timezone")
	}

	*t = timezoneValue(*loc)
	return nil
}
