package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Ssnakerss/processmonitor/internal/db"
)

type Config struct {
	// Интервал опроса процессов и активности.
	PollInterval time.Duration
	// Сколько секунд простоя считать отсутствием активности.
	IdleTimeout time.Duration
	// За сколько минут до лимита показывать предупреждение.
	NotifyBeforeMinutes int
	// Общий дневной лимит компьютера, 0 — не ограничен.
	ComputerLimitMinutes int
}

func DefaultConfig() Config {
	return Config{
		PollInterval:         5 * time.Second,
		IdleTimeout:          60 * time.Second,
		NotifyBeforeMinutes:  5,
		ComputerLimitMinutes: 0,
	}
}

// LoadConfig читает настройки из БД, подставляя значения по умолчанию.
func LoadConfig(ctx context.Context, database *db.DB) (Config, error) {
	cfg := DefaultConfig()

	if v, ok, err := database.GetConfig(ctx, "poll_interval_sec"); err == nil && ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.PollInterval = time.Duration(n) * time.Second
		}
	}
	if v, ok, err := database.GetConfig(ctx, "idle_timeout_sec"); err == nil && ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.IdleTimeout = time.Duration(n) * time.Second
		}
	}
	if v, ok, err := database.GetConfig(ctx, "notify_before_minutes"); err == nil && ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.NotifyBeforeMinutes = n
		}
	}
	if v, ok, err := database.GetConfig(ctx, "computer_daily_limit_minutes"); err == nil && ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.ComputerLimitMinutes = n
		}
	}

	return cfg, nil
}

// Save сохраняет конфигурацию в БД.
func (c Config) Save(ctx context.Context, database *db.DB) error {
	if err := database.SetConfig(ctx, "poll_interval_sec", fmt.Sprintf("%d", int(c.PollInterval.Seconds()))); err != nil {
		return err
	}
	if err := database.SetConfig(ctx, "idle_timeout_sec", fmt.Sprintf("%d", int(c.IdleTimeout.Seconds()))); err != nil {
		return err
	}
	if err := database.SetConfig(ctx, "notify_before_minutes", fmt.Sprintf("%d", c.NotifyBeforeMinutes)); err != nil {
		return err
	}
	if err := database.SetConfig(ctx, "computer_daily_limit_minutes", fmt.Sprintf("%d", c.ComputerLimitMinutes)); err != nil {
		return err
	}
	return nil
}
