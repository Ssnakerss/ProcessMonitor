package service

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Ssnakerss/processmonitor/internal/db"
	"github.com/Ssnakerss/processmonitor/internal/monitor"
	"github.com/Ssnakerss/processmonitor/internal/notifier"
)

// Service — основной цикл родительского контроля.
type Service struct {
	database *db.DB
	proc     *monitor.ProcessMonitor
	idle     *monitor.IdleTracker
	notify   notifier.Notifier

	mu      sync.RWMutex
	current Config
	today   string
	rules   []db.AppRule
	blocked map[int64]bool // rule_id -> заблокировано сегодня

	ownPID int
}

func New(database *db.DB, proc *monitor.ProcessMonitor, idle *monitor.IdleTracker, notify notifier.Notifier) *Service {
	return &Service{
		database: database,
		proc:     proc,
		idle:     idle,
		notify:   notify,
		ownPID:   os.Getpid(),
	}
}

// LoadConfig перезагружает настройки из БД на лету.
func (s *Service) LoadConfig(ctx context.Context) error {
	cfg, err := LoadConfig(ctx, s.database)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.current = cfg
	s.idle.SetIdleTimeout(cfg.IdleTimeout)
	s.mu.Unlock()
	return nil
}

// ReloadRules перезагружает правила и состояние блокировок на день.
func (s *Service) ReloadRules(ctx context.Context) error {
	return s.refreshDayState(ctx, time.Now())
}

// Run запускает основной цикл.
func (s *Service) Run(ctx context.Context) error {
	if err := s.LoadConfig(ctx); err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	s.mu.RLock()
	poll := s.current.PollInterval
	s.mu.RUnlock()

	if err := s.refreshDayState(ctx, time.Now()); err != nil {
		return fmt.Errorf("initial refresh: %w", err)
	}

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			if err := s.refreshDayState(ctx, now); err != nil {
				_ = s.database.LogEvent(ctx, "ERROR", "refresh day state", err.Error())
			}
			s.tick(ctx, now)
		}
	}
}

// refreshDayState загружает правила и блокировки для текущего дня.
func (s *Service) refreshDayState(ctx context.Context, now time.Time) error {
	today := now.Format("2006-01-02")

	s.mu.Lock()
	sameDay := today == s.today
	s.today = today
	s.mu.Unlock()

	rules, err := s.database.ListAppRules(ctx)
	if err != nil {
		return fmt.Errorf("list rules: %w", err)
	}

	blocks, err := s.database.ListBlocked(ctx, today)
	if err != nil {
		return fmt.Errorf("list blocked: %w", err)
	}

	s.mu.Lock()
	s.rules = rules
	s.blocked = blocks
	if !sameDay {
		s.proc.ClearHashCache()
		_ = s.database.LogEvent(ctx, "INFO", "new day", today)
	}
	s.mu.Unlock()

	return nil
}

func (s *Service) tick(ctx context.Context, now time.Time) {
	active, idleDur, err := s.idle.IsActive()
	if err != nil {
		_ = s.database.LogEvent(ctx, "ERROR", "idle check", err.Error())
		active = true // консервативно считаем активным
	}

	s.mu.RLock()
	cfg := s.current
	rules := s.rules
	today := s.today
	s.mu.RUnlock()

	if active {
		_ = s.database.IncrementUsage(ctx, db.RuleIDComputer, today, int(cfg.PollInterval.Seconds()))
	} else {
		_ = s.database.LogEvent(ctx, "DEBUG",
			"user idle", fmt.Sprintf("idle=%v", idleDur))
	}

	s.checkComputerLimit(ctx)

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		s.handleRule(ctx, now, rule, today, active)
	}
}

func (s *Service) checkComputerLimit(ctx context.Context) {
	s.mu.RLock()
	cfg := s.current
	today := s.today
	s.mu.RUnlock()

	if cfg.ComputerLimitMinutes <= 0 {
		return
	}

	bonus, _ := s.database.TotalBonusMinutesForDate(ctx, db.RuleIDComputer, today)
	limit := cfg.ComputerLimitMinutes + bonus

	used, _ := s.database.GetUsage(ctx, db.RuleIDComputer, today)
	usedMin := used / 60

	remain := limit - usedMin
	if remain <= 0 {
		s.notify.ComputerLimit()
	} else if remain <= cfg.NotifyBeforeMinutes {
		s.notify.WarnComputer(remain)
	}
}

func (s *Service) handleRule(ctx context.Context, now time.Time, rule db.AppRule, today string, active bool) {
	processes, err := s.proc.FindProcesses(rule.ExecName, rule.ExecHash)
	if err != nil {
		_ = s.database.LogEvent(ctx, "ERROR",
			"find process "+rule.ExecName, err.Error())
		return
	}
	if len(processes) == 0 {
		return
	}

	// Не убиваем самого себя, если вдруг правило совпало.
	processes = s.excludeOwn(processes)
	if len(processes) == 0 {
		return
	}

	re := newRuleEngine(now)
	timeOK := re.timeAllowed(rule)

	// 1. Нерабочее время/день — убиваем в любом состоянии.
	if !timeOK {
		_ = s.proc.TerminateAll(processes)
		_ = s.database.LogEvent(ctx, "BLOCK_TIME", rule.Name,
			fmt.Sprintf("pids=%v", pids(processes)))
		s.notify.AppKilled(rule.Name)
		return
	}

	// 2. Ранее исчерпан лимит — сразу убиваем, пока не выдали бонус.
	if s.isBlockedToday(rule.ID) {
		_ = s.proc.TerminateAll(processes)
		_ = s.database.LogEvent(ctx, "BLOCK_DAY", rule.Name,
			fmt.Sprintf("pids=%v", pids(processes)))
		return
	}

	// 3. Активность + разрешённое время = начисляем минуты.
	if active {
		seconds := int(s.current.PollInterval.Seconds())
		if err := s.database.IncrementUsage(ctx, rule.ID, today, seconds); err != nil {
			_ = s.database.LogEvent(ctx, "ERROR", "increment usage", err.Error())
		}
	}

	// 4. Проверяем лимит.
	used, _ := s.database.GetUsage(ctx, rule.ID, today)
	usedMin := used / 60

	bonus, _ := s.database.TotalBonusMinutesForDate(ctx, rule.ID, today)
	limit := rule.DailyLimitMinutes + bonus

	if limit <= 0 {
		// Лимит не задан — только временной контроль.
		return
	}

	remain := limit - usedMin
	if remain <= 0 {
		// Лимит исчерпан — блокируем, убиваем, уведомляем.
		_ = s.database.SetBlocked(ctx, rule.ID, today, true, "limit reached")
		_ = s.proc.TerminateAll(processes)
		_ = s.database.LogEvent(ctx, "KILL", rule.Name,
			fmt.Sprintf("pids=%v", pids(processes)))

		s.notify.AppKilled(rule.Name)
		s.markBlocked(rule.ID, true)
		return
	}

	if remain <= rule.NotifyBeforeMinutes {
		s.notify.WarnApp(rule.Name, remain)
	}
}

func (s *Service) isBlockedToday(ruleID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.blocked[ruleID]
}

func (s *Service) markBlocked(ruleID int64, v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blocked == nil {
		s.blocked = make(map[int64]bool)
	}
	s.blocked[ruleID] = v
}

func (s *Service) excludeOwn(list []monitor.ProcessInfo) []monitor.ProcessInfo {
	out := list[:0]
	for _, p := range list {
		if p.PID == s.ownPID {
			continue
		}
		out = append(out, p)
	}
	return out
}

func pids(list []monitor.ProcessInfo) []int {
	out := make([]int, len(list))
	for i, p := range list {
		out[i] = p.PID
	}
	return out
}
