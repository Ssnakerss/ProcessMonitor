package service

import (
	"encoding/json"
	"time"

	"github.com/Ssnakerss/processmonitor/internal/db"
)

// TimeWindow описывает разрешённое время в формате "HH:MM".
type TimeWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type ruleEngine struct {
	now     string // "HH:MM"
	weekDay int    // time.Weekday(): Вс=0...Сб=6
}

func newRuleEngine(now time.Time) *ruleEngine {
	return &ruleEngine{
		now:     now.Format("15:04"),
		weekDay: int(now.Weekday()),
	}
}

// weekdayAllowed проверяет строку Пн-Вс из БД ("1111111").
func (re *ruleEngine) weekdayAllowed(weekdays string) bool {
	if len(weekdays) != 7 {
		return true
	}
	// Go: Вс=0, Пн=1 -> индекс (wd+6)%7 даст Пн=0 ... Вс=6.
	idx := (re.weekDay + 6) % 7
	return weekdays[idx] == '1'
}

// windowAllowed разбирает JSON-окна и проверяет, попадает ли текущее время.
func (re *ruleEngine) windowAllowed(windowsJSON string) bool {
	if windowsJSON == "" || windowsJSON == "[]" {
		return true
	}

	var windows []TimeWindow
	if err := json.Unmarshal([]byte(windowsJSON), &windows); err != nil {
		return true
	}
	if len(windows) == 0 {
		return true
	}

	for _, w := range windows {
		if w.Start == "" || w.End == "" {
			continue
		}
		if re.now >= w.Start && re.now <= w.End {
			return true
		}
	}
	return false
}

// timeAllowed true, если сегодня разрешённый день и текущее время в окне.
func (re *ruleEngine) timeAllowed(rule db.AppRule) bool {
	if !re.weekdayAllowed(rule.Weekdays) {
		return false
	}
	return re.windowAllowed(rule.TimeWindows)
}
