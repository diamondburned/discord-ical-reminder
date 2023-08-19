package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/diamondburned/arikawa/v3/api/webhook"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/pkg/errors"
	"github.com/tj/go-naturaldate"
	"golang.org/x/sync/errgroup"
	"libdb.so/discord-ical-reminder/calendar"
	"libdb.so/discord-ical-reminder/clocker"
)

var (
	verbose    = false
	configGlob = "config*.json"
)

func init() {
	flag.BoolVar(&verbose, "v", verbose, "verbose")
	flag.StringVar(&configGlob, "c", configGlob, "config file")
}

func main() {
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	logLevel := slog.LevelWarn
	if verbose {
		logLevel = slog.LevelDebug
	}

	slog.SetDefault(slog.New(
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: logLevel,
		})))

	if err := run(ctx); err != nil && err != context.Canceled {
		log.Fatalln(err)
	}
}

func run(ctx context.Context) error {
	configFiles, err := filepath.Glob(configGlob)
	if err != nil {
		return errors.Wrap(err, "failed to glob config files")
	}

	for _, path := range configFiles {
		slog.DebugContext(ctx,
			"found config file",
			"path", path)
	}

	cfg, err := parseConfigFiles(configFiles)
	if err != nil {
		return errors.Wrap(err, "failed to parse config file")
	}

	calendars := make([]*trackedCalendar, len(cfg.Calendars))
	for i, cfg := range cfg.Calendars {
		calendar, err := newTrackedCalendar(cfg)
		if err != nil {
			return errors.Wrapf(err, "failed to create calendar %q", cfg.ICalURL)
		}
		calendars[i] = calendar
	}

	errg, ctx := errgroup.WithContext(ctx)
	defer errg.Wait()

	var refreshCh <-chan time.Time
	if cfg.RefreshFrequency > 0 {
		refreshCh = clocker.Tick(cfg.RefreshFrequency.Duration())
	}

	notifier := calendar.NewNotifier(calendar.NotifierOpts{
		EventsOpts: calendar.EventsOpts{
			DefaultReminderAction: "DISCORD",
			DefaultReminders:      durationValues(cfg.EventNotifications),
			ExcludeCancelled:      true,
			ParseReminder:         newDiscordRemindersParser(ctx),
		},
		// Don't skip past notifications in case the bot goes down.
		// Don't restart the bot too often, or else the bot will spam
		// notifications.
		SkipPastNotifications: false,
	})
	notifier.Update(func(state *calendar.NotifierState) {
		for _, calendar := range calendars {
			state.AddCalendar(calendar.Calendar)
		}
	})

	notification := make(chan calendar.Notification)
	errg.Go(func() error { return notifier.Notify(ctx, notification) })

	refreshCalendar := func(ctx context.Context) {
		var changed bool
		for _, cal := range calendars {
			u, err := cal.Calendar.Refresh(ctx)
			if err != nil {
				slog.ErrorContext(ctx,
					"failed to refresh calendar",
					"calendar", cal.Config.ICalURL,
					"error", err)
				continue
			}
			if u {
				changed = true
				slog.DebugContext(ctx,
					"calendar changed",
					"calendar", cal.Config.ICalURL)
			}
		}
		if changed {
			notifier.Invalidate()
		}
	}

	sendNotification := func(ctx context.Context, notification calendar.Notification) {
		calendar := findCalendar(calendars, notification.Calendar)
		if calendar == nil {
			slog.DebugContext(ctx,
				"drop notification for unknown calendar",
				"calendar", notification.Calendar)
			return
		}

		message, err := createNotificationMessage(calendar, notification)
		if err != nil {
			slog.ErrorContext(ctx,
				"failed to create notification message",
				"calendar", notification.Calendar,
				"error", err)
			return
		}

		// Calculate an expiration time for the context, since the notification
		// is invalid once the event starts.
		expireAfter := notification.Event.StartsAt.Sub(notification.RemindedAt)
		ctx, cancel := context.WithTimeout(ctx, expireAfter)
		defer cancel()

		webhookClient := calendar.WebhookClient.WithContext(ctx)
		if err := webhookClient.Execute(*message); err != nil {
			slog.ErrorContext(ctx,
				"failed to send notification",
				"calendar", notification.Calendar,
				"error", err)
			return
		}
	}

	errg.Go(func() error {
		refreshCalendar(ctx)
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-refreshCh:
				slog.DebugContext(ctx,
					"refreshing calendar",
					"refresh_frequency", cfg.RefreshFrequency.Duration())
				refreshCalendar(ctx)
			case notification := <-notification:
				slog.DebugContext(ctx,
					"received notification",
					"event", notification.Event.Summary,
					"starts_at", notification.Event.StartsAt,
					"reminded_at", notification.RemindedAt)
				sendNotification(ctx, notification)
			}
		}
	})

	return errg.Wait()
}

type trackedCalendar struct {
	Calendar        *calendar.OnlineICSCalendar
	WebhookClient   *webhook.Client
	MessageTemplate *template.Template
	Config          calendarConfig
}

func newTrackedCalendar(cfg calendarConfig) (*trackedCalendar, error) {
	webhookClient, err := webhook.NewFromURL(cfg.WebhookURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create webhook")
	}

	messageTemplate, err := template.New("").Parse(cfg.MessageTemplate)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse message template")
	}

	return &trackedCalendar{
		Calendar:        calendar.NewOnlineICSCalendar(cfg.ICalURL),
		WebhookClient:   webhookClient,
		MessageTemplate: messageTemplate,
		Config:          cfg,
	}, nil
}

func findCalendar(calendars []*trackedCalendar, c calendar.Calendar) *trackedCalendar {
	i := slices.IndexFunc(calendars, func(t *trackedCalendar) bool { return t.Calendar == c })
	if i == -1 {
		return nil
	}
	return calendars[i]
}

var discordReminderRe = regexp.MustCompile(`Remind on Discord (.+?) before the event\.`)

func newDiscordRemindersParser(ctx context.Context) calendar.ReminderParseFunc {
	return func(e calendar.Event) []calendar.Reminder {
		matches := discordReminderRe.FindAllStringSubmatch(e.Description, -1)
		reminders := make([]calendar.Reminder, 0, len(matches))

		for _, m := range matches {
			t, err := naturaldate.Parse(m[1], e.StartsAt, naturaldate.WithDirection(naturaldate.Past))
			if err != nil {
				slog.WarnContext(ctx,
					"failed to parse Discord reminder duration",
					"event", e.Summary,
					"duration", m[1],
					"err", err)
				continue
			}
			reminders = append(reminders, calendar.Reminder{
				Action:   "DISCORD",
				RemindAt: t,
			})
		}

		return reminders
	}
}

func createNotificationMessage(cal *trackedCalendar, notification calendar.Notification) (*webhook.ExecuteData, error) {
	description := notification.Event.Description
	description = discordReminderRe.ReplaceAllString(description, "")
	description = strings.TrimSpace(description)

	embed := discord.Embed{
		Title:       notification.Event.Summary,
		Description: description,
		Color:       0x2c91c6,
		Fields: []discord.EmbedField{
			{
				Name:   "Start Time",
				Value:  fmt.Sprintf("<t:%d:R>", notification.Event.StartsAt.Unix()),
				Inline: true,
			},
			{
				Name:   "Duration",
				Value:  humanDuration(notification.Event.StartsAt.Sub(notification.Event.EndsAt)),
				Inline: true,
			},
		},
	}
	if notification.Event.Location != "" {
		embed.Fields = append(embed.Fields, discord.EmbedField{
			Name:   "Location",
			Value:  notification.Event.Location,
			Inline: true,
		})
	}

	var content strings.Builder
	if err := cal.MessageTemplate.Execute(&content, notification); err != nil {
		return nil, errors.Wrap(err, "failed to execute message template")
	}

	return &webhook.ExecuteData{
		Content: content.String(),
		Embeds:  []discord.Embed{embed},
	}, nil
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)

	var s strings.Builder
	if d > time.Hour {
		h := d / time.Hour
		d -= h * time.Hour
		if h == 1 {
			fmt.Fprintf(&s, "%d hour ", h)
		} else {
			fmt.Fprintf(&s, "%d hours ", h)
		}
	}
	if d > time.Minute {
		m := d / time.Minute
		d -= m * time.Minute
		if m == 1 {
			fmt.Fprintf(&s, "%d minute ", m)
		} else {
			fmt.Fprintf(&s, "%d minutes ", m)
		}
	}

	return strings.TrimSpace(s.String())
}
