# discord-ical-reminder

Daemon for posting ICal reminders using Discord webhooks.

## Usage

```sh
go build
cp config.json config.local.json
# Edit config.local.json
./discord-ical-reminder -c config.local.json
```
