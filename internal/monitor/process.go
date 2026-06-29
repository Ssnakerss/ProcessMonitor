package monitor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProcessInfo — информация о найденном процессе.
type ProcessInfo struct {
	PID        int
	Name       string
	Path       string
	SHA256     string
	CreateTime time.Time
}

// ProcessMonitor умеет искать процессы по имени/хэшу и завершать их.
type ProcessMonitor struct {
	mu sync.RWMutex

	// Кэш хэшей путей, чтобы не считать SHA256 каждый poll.
	hashCache map[string]string
}

// NewProcessMonitor создаёт монитор процессов.
func NewProcessMonitor() *ProcessMonitor {
	return &ProcessMonitor{
		hashCache: make(map[string]string),
	}
}

// ClearHashCache очищает кэш хэшей.
func (pm *ProcessMonitor) ClearHashCache() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.hashCache = make(map[string]string)
}

// FindProcesses возвращает процессы, соответствующие execName.
// Если hash не пустой, дополнительно фильтрует по SHA256 файла.
func (pm *ProcessMonitor) FindProcesses(execName, execHash string) ([]ProcessInfo, error) {
	execName = strings.ToLower(filepath.Clean(execName))

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("create snapshot: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))

	if err := windows.Process32First(snapshot, &pe); err != nil {
		return nil, fmt.Errorf("process32first: %w", err)
	}

	var result []ProcessInfo
	for {
		name := strings.ToLower(syscall.UTF16ToString(pe.ExeFile[:]))

		if name == execName {
			pid := int(pe.ProcessID)
			path, err := pm.processPath(pid)
			if err != nil {
				// Системные процессы могут не отдавать путь, пропускаем.
				_ = err
			}

			info := ProcessInfo{
				PID:  pid,
				Name: name,
				Path: path,
			}

			// Проверяем хэш, только если задан.
			if execHash != "" && path != "" {
				hash, err := pm.fileHash(path)
				if err == nil {
					info.SHA256 = hash
					if strings.EqualFold(hash, execHash) {
						result = append(result, info)
					}
				}
			} else {
				result = append(result, info)
			}
		}

		if err := windows.Process32Next(snapshot, &pe); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, fmt.Errorf("process32next: %w", err)
		}
	}

	return result, nil
}

// TerminateProcess завершает процесс по PID.
func (pm *ProcessMonitor) TerminateProcess(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)

	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("terminate process %d: %w", pid, err)
	}
	return nil
}

// TerminateAll завершает все процессы из списка.
func (pm *ProcessMonitor) TerminateAll(processes []ProcessInfo) error {
	var errs []error
	for _, p := range processes {
		if err := pm.TerminateProcess(p.PID); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("terminate errors: %v", errs)
	}
	return nil
}

// processPath возвращает полный путь к исполняемому файлу процесса.
func (pm *ProcessMonitor) processPath(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", err
	}

	return syscall.UTF16ToString(buf[:size]), nil
}

// fileHash возвращает SHA256 файла, используя кэш.
func (pm *ProcessMonitor) fileHash(path string) (string, error) {
	path = strings.ToLower(path)

	pm.mu.RLock()
	if h, ok := pm.hashCache[path]; ok {
		pm.mu.RUnlock()
		return h, nil
	}
	pm.mu.RUnlock()

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	hash := hex.EncodeToString(h.Sum(nil))

	pm.mu.Lock()
	pm.hashCache[path] = hash
	pm.mu.Unlock()

	return hash, nil
}

// HashFile — вспомогательная функция для получения SHA256 файла.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
