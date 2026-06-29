package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ssnakerss/processmonitor/internal/db"
	"github.com/Ssnakerss/processmonitor/internal/service"

	"golang.org/x/crypto/bcrypt"
)

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "login.html", nil)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")

	hash, ok, err := s.db.GetConfig(r.Context(), "admin_password_hash")
	if err != nil {
		s.errorPage(w, "ошибка базы данных", http.StatusInternalServerError)
		return
	}

	// При первом запуске пароль по умолчанию — admin.
	if !ok {
		defHash, _ := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
		_ = s.db.SetConfigIfNotExists(r.Context(), "admin_password_hash", string(defHash))
		hash, _, _ = s.db.GetConfig(r.Context(), "admin_password_hash")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		s.render(w, "login.html", map[string]any{"Error": "Неверный пароль"})
		return
	}

	sid := generateSessionID()
	setSession(sid, &userSession{username: "admin", created: time.Now()})
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		deleteSession(c.Value)
		http.SetCookie(w, &http.Cookie{
			Name:   sessionCookieName,
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	today := time.Now().Format("2006-01-02")
	rules, _ := s.db.ListAppRules(r.Context())

	type ruleState struct {
		db.AppRule
		UsedMinutes  int
		BonusMinutes int
		LimitMinutes int
		Blocked      bool
	}

	states := make([]ruleState, 0, len(rules))
	for _, rule := range rules {
		used, _ := s.db.GetUsage(r.Context(), rule.ID, today)
		bonus, _ := s.db.TotalBonusMinutesForDate(r.Context(), rule.ID, today)
		blocked, _ := s.db.IsBlocked(r.Context(), rule.ID, today)
		states = append(states, ruleState{
			AppRule:      rule,
			UsedMinutes:  used / 60,
			BonusMinutes: bonus,
			LimitMinutes: rule.DailyLimitMinutes + bonus,
			Blocked:      blocked,
		})
	}

	computerUsed, _ := s.db.GetUsage(r.Context(), db.RuleIDComputer, today)
	computerBonus, _ := s.db.TotalBonusMinutesForDate(r.Context(), db.RuleIDComputer, today)

	s.render(w, "dashboard.html", map[string]any{
		"Rules":         states,
		"ComputerUsed":  computerUsed / 60,
		"ComputerBonus": computerBonus,
		"Today":         today,
	})
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	rules, _ := s.db.ListAppRules(r.Context())
	s.render(w, "rules.html", map[string]any{"Rules": rules})
}

func (s *Server) newRuleForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "rule_form.html", map[string]any{
		"Rule": db.AppRule{
			Weekdays:            "1111111",
			TimeWindows:         "[]",
			NotifyBeforeMinutes: 5,
			BlockAfterLimit:     true,
		},
	})
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	rule, ok := s.parseRuleForm(r)
	if !ok {
		s.errorPage(w, "некорректные данные", http.StatusBadRequest)
		return
	}
	if _, err := s.db.CreateAppRule(r.Context(), rule); err != nil {
		s.errorPage(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.db.LogEvent(r.Context(), "INFO", "rule created", rule.ExecName)
	_ = s.service.ReloadRules(r.Context())
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (s *Server) editRuleForm(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rule, _ := s.db.GetAppRule(r.Context(), id)
	if rule == nil {
		s.errorPage(w, "правило не найдено", http.StatusNotFound)
		return
	}
	s.render(w, "rule_form.html", map[string]any{"Rule": rule, "Edit": true})
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rule, ok := s.parseRuleForm(r)
	if !ok {
		s.errorPage(w, "некорректные данные", http.StatusBadRequest)
		return
	}
	rule.ID = id
	if err := s.db.UpdateAppRule(r.Context(), rule); err != nil {
		s.errorPage(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.db.LogEvent(r.Context(), "INFO", "rule updated", rule.ExecName)
	_ = s.service.ReloadRules(r.Context())
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = s.db.DeleteAppRule(r.Context(), id)
	_ = s.db.LogEvent(r.Context(), "INFO", "rule deleted", fmt.Sprintf("id=%d", id))
	_ = s.service.ReloadRules(r.Context())
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (s *Server) toggleRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rule, _ := s.db.GetAppRule(r.Context(), id)
	if rule == nil {
		s.errorPage(w, "правило не найдено", http.StatusNotFound)
		return
	}
	rule.Enabled = !rule.Enabled
	_ = s.db.UpdateAppRule(r.Context(), *rule)
	_ = s.service.ReloadRules(r.Context())
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (s *Server) parseRuleForm(r *http.Request) (db.AppRule, bool) {
	_ = r.ParseForm()

	name := strings.TrimSpace(r.FormValue("name"))
	execName := strings.TrimSpace(r.FormValue("exec_name"))
	execHash := strings.TrimSpace(r.FormValue("exec_hash"))
	weekdays := r.FormValue("weekdays")
	if len(weekdays) != 7 {
		weekdays = "1111111"
	}
	windows := r.FormValue("time_windows")
	if windows == "" {
		windows = "[]"
	}
	limit, _ := strconv.Atoi(r.FormValue("daily_limit_minutes"))
	notify, _ := strconv.Atoi(r.FormValue("notify_before_minutes"))
	block := r.FormValue("block_after_limit") == "on"

	if name == "" || execName == "" {
		return db.AppRule{}, false
	}

	return db.AppRule{
		Name:                name,
		ExecName:            execName,
		ExecHash:            execHash,
		Weekdays:            weekdays,
		TimeWindows:         windows,
		DailyLimitMinutes:   limit,
		NotifyBeforeMinutes: notify,
		BlockAfterLimit:     block,
	}, true
}

func (s *Server) configForm(w http.ResponseWriter, r *http.Request) {
	cfg, _ := service.LoadConfig(r.Context(), s.db)
	port := "8080"
	bind := "0.0.0.0"
	if v, ok, _ := s.db.GetConfig(r.Context(), "web_port"); ok {
		port = v
	}
	if v, ok, _ := s.db.GetConfig(r.Context(), "web_bind"); ok {
		bind = v
	}

	s.render(w, "config.html", map[string]any{
		"Config": cfg,
		"Port":   port,
		"Bind":   bind,
	})
}

func (s *Server) saveConfig(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()

	cfg := service.Config{
		PollInterval:         time.Duration(atoi(r.FormValue("poll_interval_sec"))) * time.Second,
		IdleTimeout:          time.Duration(atoi(r.FormValue("idle_timeout_sec"))) * time.Second,
		NotifyBeforeMinutes:  atoi(r.FormValue("notify_before_minutes")),
		ComputerLimitMinutes: atoi(r.FormValue("computer_daily_limit_minutes")),
	}
	if err := cfg.Save(r.Context(), s.db); err != nil {
		s.errorPage(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.db.SetConfig(r.Context(), "web_port", r.FormValue("web_port")); err != nil {
		s.errorPage(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.db.SetConfig(r.Context(), "web_bind", r.FormValue("web_bind")); err != nil {
		s.errorPage(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if pwd := r.FormValue("admin_password"); pwd != "" {
		hash, _ := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
		_ = s.db.SetConfig(r.Context(), "admin_password_hash", string(hash))
	}

	_ = s.db.LogEvent(r.Context(), "INFO", "config updated", "")
	_ = s.service.LoadConfig(r.Context())
	_ = s.service.ReloadRules(r.Context())

	http.Redirect(w, r, "/config", http.StatusSeeOther)
}

func (s *Server) addBonus(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	minutes := atoi(r.FormValue("minutes"))
	note := r.FormValue("note")
	today := time.Now().Format("2006-01-02")

	if minutes > 0 {
		_, _ = s.db.AddBonus(r.Context(), id, today, minutes, note)
		_ = s.db.UnblockRule(r.Context(), id, today)
		_ = s.db.LogEvent(r.Context(), "BONUS",
			fmt.Sprintf("+%d min rule %d", minutes, id), note)
	}
	_ = s.service.ReloadRules(r.Context())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) addComputerBonus(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	minutes := atoi(r.FormValue("minutes"))
	note := r.FormValue("note")
	today := time.Now().Format("2006-01-02")

	if minutes > 0 {
		_, _ = s.db.AddBonus(r.Context(), db.RuleIDComputer, today, minutes, note)
		_ = s.db.LogEvent(r.Context(), "BONUS",
			fmt.Sprintf("+%d min computer", minutes), note)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) unblockRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	today := time.Now().Format("2006-01-02")
	_ = s.db.UnblockRule(r.Context(), id, today)
	_ = s.db.LogEvent(r.Context(), "UNBLOCK", fmt.Sprintf("rule %d", id), "")
	_ = s.service.ReloadRules(r.Context())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) showLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 {
		limit = n
	}
	events, _ := s.db.RecentEvents(r.Context(), limit)
	s.render(w, "logs.html", map[string]any{"Events": events})
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
