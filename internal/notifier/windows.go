package notifier

import (
	"fmt"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	moduser32       = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW = moduser32.NewProc("MessageBoxW")
	procMessageBeep = moduser32.NewProc("MessageBeep")

	toastMu sync.Mutex
)

const (
	mbIconWarning = 0x30
	mbOk          = 0x00
	beepWarning   = 0x30 // MB_ICONEXCLAMATION
)

// Beep воспроизводит системный звук.
func (n *windowsNotifier) Beep() {
	procMessageBeep.Call(uintptr(beepWarning))
}

// showToast показывает Windows 10/11 toast через PowerShell + WinRT.
// Запускается асинхронно, чтобы не блокировать монитор.
func (n *windowsNotifier) showToast(title, message string) error {
	// PowerShell here-string с XML-экранированием через SecurityElement.Escape.
	const script = `
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
$title = $args[0]
$message = $args[1]
$template = @"
<toast>
  <visual>
    <binding template="ToastText02">
      <text id="1">$([System.Security.SecurityElement]::Escape($title))</text>
      <text id="2">$([System.Security.SecurityElement]::Escape($message))</text>
    </binding>
  </visual>
</toast>
"@
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($template)
$toast = New-Object Windows.UI.Notifications.ToastNotification($xml)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("Parental Control").Show($toast)
`
	cmd := exec.Command(
		"powershell.exe",
		"-NoProfile",
		"-ExecutionPolicy", "Bypass",
		"-Command", script,
		title,
		message,
	)

	// Запускаем асинхронно.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start powershell toast: %w", err)
	}
	return nil
}

// showMessageBox показывает модальное окно с кнопкой OK.
// Вызов идёт в отдельной горутине, чтобы не блокировать основной цикл.
func (n *windowsNotifier) showMessageBox(title, message string) {
	go func() {
		toastMu.Lock()
		defer toastMu.Unlock()

		t, err := windows.UTF16PtrFromString(title)
		if err != nil {
			return
		}
		m, err := windows.UTF16PtrFromString(message)
		if err != nil {
			return
		}

		procMessageBoxW.Call(
			0,
			uintptr(unsafe.Pointer(m)),
			uintptr(unsafe.Pointer(t)),
			uintptr(mbIconWarning|mbOk),
		)
	}()
}
