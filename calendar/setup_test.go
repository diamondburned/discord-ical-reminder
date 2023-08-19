package calendar

import (
	"log/slog"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})))
	os.Exit(m.Run())
}
