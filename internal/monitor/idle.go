package monitor

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	moduser32            = syscall.NewLazyDLL("user32.dll")
	procGetLastInputInfo = moduser32.NewProc("GetLastInputInfo")
)

// LASTINPUTINFO — структура для GetLastInputInfo.
type LASTINPUTINFO struct {
	cbSize uint32
	dwTime uint32
}

// IdleTracker отслеживает время бездействия пользователя.
type IdleTracker struct {
	idleTimeout time.Duration
}

// NewIdleTracker создаёт трекер с заданным таймаутом бездействия.
func NewIdleTracker(idleTimeout time.Duration) *IdleTracker {
	return &IdleTracker{idleTimeout: idleTimeout}
}

// SetIdleTimeout позволяет менять таймаут на лету.
func (t *IdleTracker) SetIdleTimeout(d time.Duration) {
	t.idleTimeout = d
}

// IdleDuration возвращает время с последнего ввода.
func (t *IdleTracker) IdleDuration() (time.Duration, error) {
	var li LASTINPUTINFO
	li.cbSize = uint32(unsafe.Sizeof(li))

	ret, _, err := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&li)))
	if ret == 0 {
		return 0, err
	}

	// GetTickCount64 не переполняется, в отличие от dwTime.
	tickCount, _, err := procGetTickCount64.Call()
	if tickCount == 0 {
		return 0, err
	}

	idleMs := uint64(tickCount) - uint64(li.dwTime)
	return time.Duration(idleMs) * time.Millisecond, nil
}

// IsIdle возвращает true, если пользователь неактивен дольше таймаута.
func (t *IdleTracker) IsIdle() (bool, time.Duration, error) {
	d, err := t.IdleDuration()
	if err != nil {
		return false, 0, err
	}
	return d >= t.idleTimeout, d, nil
}

// IsActive — обратная функция, удобна в мониторе.
func (t *IdleTracker) IsActive() (bool, time.Duration, error) {
	idle, d, err := t.IsIdle()
	if err != nil {
		return false, 0, err
	}
	return !idle, d, nil
}

var (
	modkernel32        = syscall.NewLazyDLL("kernel32.dll")
	procGetTickCount64 = modkernel32.NewProc("GetTickCount64")
)
