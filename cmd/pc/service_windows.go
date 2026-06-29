package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/Ssnakerss/processmonitor/internal/service"
	"github.com/Ssnakerss/processmonitor/internal/web"

	winsvc "golang.org/x/sys/windows/svc"
)

// program — Windows-специфическая обёртка, удовлетворяющая winsvc.Handler.
type program struct {
	svc *service.Service
	web *web.Server
}

func (p *program) Execute(args []string, r <-chan winsvc.ChangeRequest, changes chan<- winsvc.Status) (ssec bool, errno uint32) {
	// Загружаем актуальную конфигурацию перед стартом.
	_ = p.svc.LoadConfig(context.Background())

	changes <- winsvc.Status{State: winsvc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_ = p.svc.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		_ = p.web.Run(ctx)
	}()

	changes <- winsvc.Status{
		State:   winsvc.Running,
		Accepts: winsvc.AcceptStop | winsvc.AcceptShutdown,
	}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case winsvc.Interrogate:
				changes <- c.CurrentStatus
			case winsvc.Stop, winsvc.Shutdown:
				changes <- winsvc.Status{State: winsvc.StopPending}
				cancel()
				wg.Wait()
				changes <- winsvc.Status{State: winsvc.Stopped}
				return false, 0
			}
		}
	}
}

func runService() {
	d, err := openDB()
	if err != nil {
		slogError(err)
		os.Exit(1)
	}
	defer d.Close()

	svc, webServer, err := buildServices(d)
	if err != nil {
		slogError(err)
		os.Exit(1)
	}

	p := &program{
		svc: svc,
		web: webServer,
	}

	if err := winsvc.Run(serviceName, p); err != nil {
		slogError(err)
		os.Exit(1)
	}
}

// Утилита для логирования внутри сервиса без slog-инициализации.
func slogError(err error) {
	fmt.Fprintf(os.Stderr, "[error] %v\n", err)
}
