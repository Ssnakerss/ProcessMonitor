package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/Ssnakerss/processmonitor/internal/db"
	"github.com/Ssnakerss/processmonitor/internal/monitor"
	"github.com/Ssnakerss/processmonitor/internal/notifier"
	parentservice "github.com/Ssnakerss/processmonitor/internal/service"
	"github.com/Ssnakerss/processmonitor/internal/web"
)

const (
	serviceName    = "ParentalControl"
	serviceDisplay = "Parental Control"
	serviceDesc    = "Ограничение времени использования приложений и компьютера"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			if err := installService(serviceName, serviceDisplay, serviceDesc); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Println("Служба установлена")
			return
		case "uninstall":
			if err := uninstallService(serviceName); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Println("Служба удалена")
			return
		}
	}

	isInteractive, _ := svc.IsAnInteractiveSession()
	if len(os.Args) > 1 && os.Args[1] == "run" {
		isInteractive = true
	}

	if isInteractive {
		runConsole()
	} else {
		runService()
	}
}

// --------------------------------------------------------------------------
// База данных и значения по умолчанию
// --------------------------------------------------------------------------

func openDB() (*db.DB, error) {
	path := getDBPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	d, err := db.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := ensureDefaults(context.Background(), d); err != nil {
		return nil, fmt.Errorf("defaults: %w", err)
	}
	return d, nil
}

func getDBPath() string {
	// Лучше хранить БД в ProgramData, чтобы она была доступна службе SYSTEM.
	if v := os.Getenv("PROGRAMDATA"); v != "" {
		return filepath.Join(v, "ParentalControl", "data.db")
	}
	// Fallback — папка с бинарником.
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "data.db")
}

func ensureDefaults(ctx context.Context, d *db.DB) error {
	if err := d.SetConfigIfNotExists(ctx, "web_bind", "0.0.0.0"); err != nil {
		return err
	}
	if err := d.SetConfigIfNotExists(ctx, "web_port", "8080"); err != nil {
		return err
	}
	if err := d.SetConfigIfNotExists(ctx, "poll_interval_sec", "5"); err != nil {
		return err
	}
	if err := d.SetConfigIfNotExists(ctx, "idle_timeout_sec", "60"); err != nil {
		return err
	}
	if err := d.SetConfigIfNotExists(ctx, "notify_before_minutes", "5"); err != nil {
		return err
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err := d.SetConfigIfNotExists(ctx, "admin_password_hash", string(hash)); err != nil {
		return err
	}
	return nil
}

// --------------------------------------------------------------------------
// Инициализация сервисов
// --------------------------------------------------------------------------

func buildServices(d *db.DB) (*parentservice.Service, *web.Server, error) {
	cfg, err := parentservice.LoadConfig(context.Background(), d)
	if err != nil {
		// Если конфиг не загрузился, используем значения по умолчанию.
		cfg = parentservice.DefaultConfig()
	}

	proc := monitor.NewProcessMonitor()
	idle := monitor.NewIdleTracker(cfg.IdleTimeout)
	n := notifier.NewWindowsNotifier(notifier.RateLimitConfig{
		MinInterval: 5 * time.Minute,
	})

	svc := parentservice.New(d, proc, idle, n)
	webServer, err := web.New(d, svc)
	if err != nil {
		return nil, nil, fmt.Errorf("web init: %w", err)
	}

	return svc, webServer, nil
}

// --------------------------------------------------------------------------
// Консольный режим (run / отладка)
// --------------------------------------------------------------------------

func runConsole() {
	d, err := openDB()
	if err != nil {
		slog.Error("open db", slog.Any("error", err))
		os.Exit(1)
	}
	defer d.Close()

	svc, webServer, err := buildServices(d)
	if err != nil {
		slog.Error("build services", slog.Any("error", err))
		os.Exit(1)
	}

	if err := svc.LoadConfig(context.Background()); err != nil {
		slog.Warn("load config", slog.Any("error", err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	slog.Info("starting console mode")

	go func() { _ = svc.Run(ctx) }()
	go func() { _ = webServer.Run(ctx) }()

	<-ctx.Done()
	slog.Info("shutting down")
}

// --------------------------------------------------------------------------
// Установка / удаление службы
// --------------------------------------------------------------------------

func installService(name, displayName, desc string) error {
	exepath, err := os.Executable()
	if err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to scm: %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(name); err == nil {
		s.Close()
		return fmt.Errorf("служба %s уже установлена", name)
	}

	cfg := mgr.Config{
		DisplayName: displayName,
		Description: desc,
		StartType:   mgr.StartAutomatic,
	}
	s, err := m.CreateService(name, exepath, cfg)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	// Защита от случайного/намеренного закрытия: перезапуск при 3 сбоях,
	// сброс счётчика ошибок раз в сутки.
	recoveryActions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}
	if err := s.SetRecoveryActions(recoveryActions, 24*60*60); err != nil {
		return fmt.Errorf("set recovery: %w", err)
	}

	return nil
}

func uninstallService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to scm: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()

	// Останавливаем, если запущена; ошибки игнорируем.
	_, _ = s.Control(svc.Stop)
	return s.Delete()
}
