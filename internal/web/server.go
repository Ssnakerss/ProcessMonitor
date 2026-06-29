package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	"github.com/Ssnakerss/processmonitor/internal/db"
	"github.com/Ssnakerss/processmonitor/internal/service"
)

//go:embed templates/*.html
var templatesFS embed.FS

type Server struct {
	db      *db.DB
	service *service.Service
	tmpl    *template.Template
	mux     *http.ServeMux
}

func New(database *db.DB, svc *service.Service) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	s := &Server{
		db:      database,
		service: svc,
		tmpl:    tmpl,
		mux:     http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

// Run читает веб-настройки из БД и запускает HTTP-сервер.
func (s *Server) Run(ctx context.Context) error {
	port := 8080
	bind := "0.0.0.0"

	if v, ok, _ := s.db.GetConfig(ctx, "web_port"); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			port = n
		}
	}
	if v, ok, _ := s.db.GetConfig(ctx, "web_bind"); ok {
		bind = v
	}

	addr := fmt.Sprintf("%s:%d", bind, port)
	srv := &http.Server{
		Addr:    addr,
		Handler: s.mux,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) errorPage(w http.ResponseWriter, msg string, code int) {
	w.WriteHeader(code)
	s.render(w, "error.html", map[string]string{"Message": msg})
}

func (s *Server) routes() {
	// Авторизация
	s.mux.HandleFunc("GET /login", s.loginPage)
	s.mux.HandleFunc("POST /login", s.login)
	s.mux.HandleFunc("GET /logout", s.logout)

	// Защищённые маршруты
	s.mux.Handle("GET /{$}", s.requireAuth(http.HandlerFunc(s.dashboard)))
	s.mux.Handle("GET /rules", s.requireAuth(http.HandlerFunc(s.listRules)))
	s.mux.Handle("GET /rules/new", s.requireAuth(http.HandlerFunc(s.newRuleForm)))
	s.mux.Handle("POST /rules", s.requireAuth(http.HandlerFunc(s.createRule)))
	s.mux.Handle("GET /rules/{id}/edit", s.requireAuth(http.HandlerFunc(s.editRuleForm)))
	s.mux.Handle("POST /rules/{id}", s.requireAuth(http.HandlerFunc(s.updateRule)))
	s.mux.Handle("POST /rules/{id}/delete", s.requireAuth(http.HandlerFunc(s.deleteRule)))
	s.mux.Handle("POST /rules/{id}/toggle", s.requireAuth(http.HandlerFunc(s.toggleRule)))

	s.mux.Handle("GET /config", s.requireAuth(http.HandlerFunc(s.configForm)))
	s.mux.Handle("POST /config", s.requireAuth(http.HandlerFunc(s.saveConfig)))

	s.mux.Handle("POST /rules/{id}/bonus", s.requireAuth(http.HandlerFunc(s.addBonus)))
	s.mux.Handle("POST /bonus/computer", s.requireAuth(http.HandlerFunc(s.addComputerBonus)))
	s.mux.Handle("POST /rules/{id}/unblock", s.requireAuth(http.HandlerFunc(s.unblockRule)))

	s.mux.Handle("GET /logs", s.requireAuth(http.HandlerFunc(s.showLogs)))
}
