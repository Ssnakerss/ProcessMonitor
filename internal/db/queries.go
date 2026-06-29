package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ============================================================================
// Конфигурация
// ============================================================================

// SetConfig сохраняет строковое значение настройки.
func (d *DB) SetConfig(ctx context.Context, key, value string) error {
	_, err := d.conn.ExecContext(ctx, `
        INSERT INTO config (key, value, updated_at)
        VALUES (?, ?, CURRENT_TIMESTAMP)
        ON CONFLICT(key) DO UPDATE SET
            value = excluded.value,
            updated_at = CURRENT_TIMESTAMP
    `, key, value)
	return err
}

// SetConfigIfNotExists устанавливает значение, только если его ещё нет.
func (d *DB) SetConfigIfNotExists(ctx context.Context, key, value string) error {
	_, err := d.conn.ExecContext(ctx, `
        INSERT OR IGNORE INTO config (key, value) VALUES (?, ?)
    `, key, value)
	return err
}

// GetConfig возвращает строковое значение. Если ключа нет — exists=false.
func (d *DB) GetConfig(ctx context.Context, key string) (value string, exists bool, err error) {
	err = d.conn.QueryRowContext(ctx,
		"SELECT value FROM config WHERE key = ?",
		key,
	).Scan(&value)

	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// GetAllConfig возвращает весь конфиг как map.
func (d *DB) GetAllConfig(ctx context.Context) (map[string]string, error) {
	rows, err := d.conn.QueryContext(ctx, "SELECT key, value FROM config")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cfg := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		cfg[k] = v
	}
	return cfg, rows.Err()
}

// ============================================================================
// Правила приложений
// ============================================================================

type AppRule struct {
	ID                  int64
	Name                string
	Enabled             bool
	ExecName            string
	ExecHash            string
	Weekdays            string // "1111111" Пн-Вс
	TimeWindows         string // JSON
	DailyLimitMinutes   int
	NotifyBeforeMinutes int
	BlockAfterLimit     bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (d *DB) ListAppRules(ctx context.Context) ([]AppRule, error) {
	rows, err := d.conn.QueryContext(ctx, `
        SELECT id, name, enabled, exec_name, COALESCE(exec_hash,''),
               weekdays, time_windows, daily_limit_minutes,
               notify_before_minutes, block_after_limit,
               created_at, updated_at
        FROM app_rules
        ORDER BY name
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AppRule
	for rows.Next() {
		var r AppRule
		var enabled, block int
		if err := rows.Scan(
			&r.ID, &r.Name, &enabled, &r.ExecName, &r.ExecHash,
			&r.Weekdays, &r.TimeWindows, &r.DailyLimitMinutes,
			&r.NotifyBeforeMinutes, &block,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		r.Enabled = enabled == 1
		r.BlockAfterLimit = block == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) GetAppRule(ctx context.Context, id int64) (*AppRule, error) {
	var r AppRule
	var enabled, block int
	err := d.conn.QueryRowContext(ctx, `
        SELECT id, name, enabled, exec_name, COALESCE(exec_hash,''),
               weekdays, time_windows, daily_limit_minutes,
               notify_before_minutes, block_after_limit,
               created_at, updated_at
        FROM app_rules
        WHERE id = ?
    `, id).Scan(
		&r.ID, &r.Name, &enabled, &r.ExecName, &r.ExecHash,
		&r.Weekdays, &r.TimeWindows, &r.DailyLimitMinutes,
		&r.NotifyBeforeMinutes, &block,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Enabled = enabled == 1
	r.BlockAfterLimit = block == 1
	return &r, nil
}

func (d *DB) CreateAppRule(ctx context.Context, r AppRule) (int64, error) {
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	block := 0
	if r.BlockAfterLimit {
		block = 1
	}

	res, err := d.conn.ExecContext(ctx, `
        INSERT INTO app_rules (
            name, enabled, exec_name, exec_hash, weekdays,
            time_windows, daily_limit_minutes, notify_before_minutes, block_after_limit
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `,
		r.Name, enabled, r.ExecName, r.ExecHash, r.Weekdays,
		r.TimeWindows, r.DailyLimitMinutes, r.NotifyBeforeMinutes, block,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) UpdateAppRule(ctx context.Context, r AppRule) error {
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	block := 0
	if r.BlockAfterLimit {
		block = 1
	}

	_, err := d.conn.ExecContext(ctx, `
        UPDATE app_rules SET
            name = ?,
            enabled = ?,
            exec_name = ?,
            exec_hash = ?,
            weekdays = ?,
            time_windows = ?,
            daily_limit_minutes = ?,
            notify_before_minutes = ?,
            block_after_limit = ?,
            updated_at = CURRENT_TIMESTAMP
        WHERE id = ?
    `,
		r.Name, enabled, r.ExecName, r.ExecHash, r.Weekdays,
		r.TimeWindows, r.DailyLimitMinutes, r.NotifyBeforeMinutes, block,
		r.ID,
	)
	return err
}

func (d *DB) DeleteAppRule(ctx context.Context, id int64) error {
	_, err := d.conn.ExecContext(ctx, "DELETE FROM app_rules WHERE id = ?", id)
	return err
}

// ============================================================================
// Учёт активного времени
// ============================================================================

const RuleIDComputer int64 = 0

// IncrementUsage добавляет активные секунды для правила/даты.
// Если записи ещё нет — создаёт.
func (d *DB) IncrementUsage(ctx context.Context, ruleID int64, date string, seconds int) error {
	_, err := d.conn.ExecContext(ctx, `
        INSERT INTO usage_daily (rule_id, date, active_seconds, last_update)
        VALUES (?, ?, ?, CURRENT_TIMESTAMP)
        ON CONFLICT(rule_id, date) DO UPDATE SET
            active_seconds = active_seconds + excluded.active_seconds,
            last_update = CURRENT_TIMESTAMP
    `, ruleID, date, seconds)
	return err
}

// GetUsage возвращает накопленные активные секунды за дату.
func (d *DB) GetUsage(ctx context.Context, ruleID int64, date string) (int, error) {
	var sec int
	err := d.conn.QueryRowContext(ctx, `
        SELECT COALESCE(active_seconds, 0)
        FROM usage_daily
        WHERE rule_id = ? AND date = ?
    `, ruleID, date).Scan(&sec)

	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return sec, err
}

// ============================================================================
// Бонусное время
// ============================================================================

func (d *DB) AddBonus(ctx context.Context, ruleID int64, date string, minutes int, note string) (int64, error) {
	res, err := d.conn.ExecContext(ctx, `
        INSERT INTO bonus_time (rule_id, date, extra_minutes, note)
        VALUES (?, ?, ?, ?)
    `, ruleID, date, minutes, note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// TotalBonusMinutesForDate возвращает сумму выданных (и ещё не израсходованных)
// бонусных минут за конкретный день.
func (d *DB) TotalBonusMinutesForDate(ctx context.Context, ruleID int64, date string) (int, error) {
	var total sql.NullInt64
	err := d.conn.QueryRowContext(ctx, `
        SELECT COALESCE(SUM(extra_minutes - used_minutes), 0)
        FROM bonus_time
        WHERE rule_id = ? AND date = ?
    `, ruleID, date).Scan(&total)

	if err != nil {
		return 0, err
	}
	return int(total.Int64), nil
}

// ConsumeBonus увеличивает used_minutes у наиболее старых бонусов, пока не
// наберётся нужное количество минут. Обычно вызывается при исчерпании лимита.
func (d *DB) ConsumeBonus(ctx context.Context, ruleID int64, date string, minutes int) error {
	if minutes <= 0 {
		return nil
	}

	rows, err := d.conn.QueryContext(ctx, `
        SELECT id, extra_minutes - used_minutes AS remaining
        FROM bonus_time
        WHERE rule_id = ? AND date = ? AND remaining > 0
        ORDER BY granted_at
    `, ruleID, date)
	if err != nil {
		return err
	}
	defer rows.Close()

	type item struct {
		id        int64
		remaining int
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.remaining); err != nil {
			return err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	left := minutes
	for _, it := range items {
		if left <= 0 {
			break
		}
		take := it.remaining
		if take > left {
			take = left
		}
		if _, err := d.conn.ExecContext(ctx, `
            UPDATE bonus_time
            SET used_minutes = used_minutes + ?
            WHERE id = ?
        `, take, it.id); err != nil {
			return err
		}
		left -= take
	}
	return nil
}

// ============================================================================
// Логи
// ============================================================================

type Event struct {
	ID        int64
	CreatedAt time.Time
	Type      string
	Message   string
	Details   string
}

func (d *DB) LogEvent(ctx context.Context, eventType, message, details string) error {
	_, err := d.conn.ExecContext(ctx, `
        INSERT INTO events (type, message, details)
        VALUES (?, ?, ?)
    `, eventType, message, details)
	return err
}

// ============================================================================
// Блокировки приложений по дням
// ============================================================================

func (d *DB) SetBlocked(ctx context.Context, ruleID int64, date string, blocked bool, reason string) error {
	b := 0
	if blocked {
		b = 1
	}

	_, err := d.conn.ExecContext(ctx, `
        INSERT INTO blocks (rule_id, date, blocked, reason)
        VALUES (?, ?, ?, ?)
        ON CONFLICT(rule_id, date) DO UPDATE SET
            blocked = excluded.blocked,
            reason = excluded.reason
    `, ruleID, date, b, reason)
	return err
}

func (d *DB) IsBlocked(ctx context.Context, ruleID int64, date string) (bool, error) {
	var b int
	err := d.conn.QueryRowContext(ctx,
		"SELECT blocked FROM blocks WHERE rule_id = ? AND date = ?",
		ruleID, date,
	).Scan(&b)

	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return b == 1, err
}

func (d *DB) ListBlocked(ctx context.Context, date string) (map[int64]bool, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT rule_id, blocked FROM blocks WHERE date = ?", date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		var b int
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		out[id] = b == 1
	}
	return out, rows.Err()
}

func (d *DB) UnblockRule(ctx context.Context, ruleID int64, date string) error {
	_, err := d.conn.ExecContext(ctx,
		"DELETE FROM blocks WHERE rule_id = ? AND date = ?",
		ruleID, date,
	)
	return err
}

func (d *DB) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := d.conn.QueryContext(ctx, `
        SELECT id, created_at, type, message, details
        FROM events
        ORDER BY id DESC
        LIMIT ?
    `, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.Type, &e.Message, &e.Details); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
