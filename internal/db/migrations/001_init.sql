-- Метаданные миграций
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    applied_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Глобальные настройки (порт, пароли, таймауты)
CREATE TABLE IF NOT EXISTS config (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Правила для приложений
CREATE TABLE IF NOT EXISTS app_rules (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    name                    TEXT NOT NULL,
    enabled                 INTEGER NOT NULL DEFAULT 1,  -- 0/1
    exec_name               TEXT NOT NULL,                -- например "notepad.exe"
    exec_hash               TEXT,                         -- опциональный SHA256
    weekdays                TEXT NOT NULL DEFAULT '1111111', -- Пн...Вс, 1/0
    time_windows            TEXT NOT NULL DEFAULT '[]',   -- JSON [{start,end}]
    daily_limit_minutes     INTEGER NOT NULL DEFAULT 0,
    notify_before_minutes   INTEGER NOT NULL DEFAULT 5,
    block_after_limit       INTEGER NOT NULL DEFAULT 1,  -- 0 = только предупреждение
    created_at              DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at              DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_app_rules_exec ON app_rules(exec_name);

-- Учёт активного времени. rule_id = 0 означает "общий компьютер"
CREATE TABLE IF NOT EXISTS usage_daily (
    rule_id         INTEGER NOT NULL,
    date            TEXT NOT NULL,  -- YYYY-MM-DD
    active_seconds  INTEGER NOT NULL DEFAULT 0,
    last_update     DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (rule_id, date)
);

-- Бонусное время, выданное родителем на текущий день
CREATE TABLE IF NOT EXISTS bonus_time (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_id         INTEGER NOT NULL,  -- 0 = общий компьютер
    date            TEXT NOT NULL,     -- YYYY-MM-DD
    granted_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    extra_minutes   INTEGER NOT NULL,
    used_minutes    INTEGER NOT NULL DEFAULT 0,
    note            TEXT
);

CREATE INDEX IF NOT EXISTS idx_bonus_rule_date ON bonus_time(rule_id, date);

-- Логи
CREATE TABLE IF NOT EXISTS events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    type        TEXT NOT NULL,   -- INFO, WARN, BLOCK, KILL, BONUS ...
    message     TEXT,
    details     TEXT             -- JSON с pid, путём и т.д.
);

CREATE INDEX IF NOT EXISTS idx_events_created ON events(created_at);