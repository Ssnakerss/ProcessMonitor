package notifier

import (
	"sync"
	"time"
)

// Notifier описывает способ сообщить пользователю о событии.
// Методы не возвращают ошибки, потому что уведомление — необязательная
// подсказка; ошибки логируются внутри реализации.
type Notifier interface {
	// WarnApp — предупреждение о том, что у приложения заканчивается лимит.
	WarnApp(appName string, remainingMinutes int)

	// WarnComputer — предупреждение о том, что у компьютера заканчивается лимит.
	WarnComputer(remainingMinutes int)

	// AppKilled — сообщение о том, что приложение было закрыто по лимиту.
	AppKilled(appName string)

	// ComputerLimit — сообщение о достижении общего лимита компьютера.
	ComputerLimit()

	// Beep — короткий звуковой сигнал.
	Beep()
}

// RateLimitConfig управляет частотой уведомлений.
type RateLimitConfig struct {
	// MinInterval — минимальное время между двумя одинаковыми уведомлениями.
	MinInterval time.Duration
}

type windowsNotifier struct {
	mu          sync.Mutex
	minInterval time.Duration
	last        map[string]time.Time
}

// NewWindowsNotifier возвращает Notifier для Windows.
// minInterval защищает от спама при каждом poll (например, 5–10 минут).
func NewWindowsNotifier(cfg RateLimitConfig) Notifier {
	d := cfg.MinInterval
	if d <= 0 {
		d = 5 * time.Minute
	}
	return &windowsNotifier{
		minInterval: d,
		last:        make(map[string]time.Time),
	}
}

// rateLimitedSend вызывает fn только если с последнего вызова с тем же key
// прошло не менее minInterval.
func (n *windowsNotifier) rateLimitedSend(key string, fn func()) {
	n.mu.Lock()
	last := n.last[key]
	if time.Since(last) < n.minInterval {
		n.mu.Unlock()
		return
	}
	n.last[key] = time.Now()
	n.mu.Unlock()

	n.Beep()
	fn()
}

func (n *windowsNotifier) WarnApp(appName string, remainingMinutes int) {
	key := "warn:" + appName
	title := appName
	body := "Осталось " + formatMinutes(remainingMinutes)
	n.rateLimitedSend(key, func() {
		_ = n.showToast(title, body)
	})
}

func (n *windowsNotifier) WarnComputer(remainingMinutes int) {
	const key = "warn:computer"
	n.rateLimitedSend(key, func() {
		_ = n.showToast("Время компьютера", "Осталось "+formatMinutes(remainingMinutes))
	})
}

func (n *windowsNotifier) AppKilled(appName string) {
	key := "killed:" + appName
	n.rateLimitedSend(key, func() {
		n.showMessageBox(appName, "Лимит времени исчерпан. Приложение закрыто.")
	})
}

func (n *windowsNotifier) ComputerLimit() {
	const key = "limit:computer"
	n.rateLimitedSend(key, func() {
		n.showMessageBox("Время компьютера", "Дневной лимит исчерпан.")
	})
}

func formatMinutes(m int) string {
	if m <= 0 {
		return "менее минуты"
	}
	if m%10 == 1 && m%100 != 11 {
		return "1 минута"
	}
	if m%10 >= 2 && m%10 <= 4 && (m%100 < 10 || m%100 >= 20) {
		return "минуты"
	}
	return "минут"
}
