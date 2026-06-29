package models

// AppRule — целевое приложение и его лимиты.
type AppRule struct {
	ID                   int64
	Name                 string
	ProcessName          string
	PathPattern          *string // nullable
	FileHash             *string // nullable
	CheckHash            int     // 0=только имя, 1=путь+имя, 2=хэш
	DailyLimitMinutes    int
	WarningBeforeMinutes int
	IsActive             bool
	CreatedAt            int64 // unix timestamp
}

// AppSchedule — временные окна по дням недели (0=Вс ... 6=Сб).
type AppSchedule struct {
	ID        int64
	RuleID    int64
	DayOfWeek int
	TimeStart string // HH:MM
	TimeEnd   string // HH:MM
}

// DailyUsage — потреблённое/бонусное время за день по конкретному приложению.
type DailyUsage struct {
	ID           int64
	RuleID       int64
	Date         string // YYYY-MM-DD
	UsedSeconds  int
	BonusMinutes int
	IsBlocked    bool
	WarnSent     bool
}

// ComputerUsage — общий таймер активности ПК за день (только предупреждение).
type ComputerUsage struct {
	ID            int64
	Date          string
	ActiveSeconds int
	BonusMinutes  int
	Warned        bool
}

// ActiveSession — текущий процесс, за которым ведётся учёт в реальном времени.
type ActiveSession struct {
	ID          int64
	RuleID      *int64
	PID         uint32
	ProcessName string
	StartedAt   int64
	LastTickAt  int64
}

// EventLog — журнал событий.
type EventLog struct {
	ID        int64
	CreatedAt int64
	EventType string
	RuleID    *int64
	PID       *uint32
	Message   string
	Details   string // JSON или произвольный текст
}

// Setting — пара ключ/значение для конфигурации.
type Setting struct {
	Key   string
	Value string
}
