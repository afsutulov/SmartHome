package main

import (
	"errors"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// telegramMaxUploadBytes — ограничение Bot API на загрузку файла ботом (50 МБ).
const telegramMaxUploadBytes = 50 << 20

// RunSchedules каждую секунду проверяет расписание и запускает нужные действия один раз в сутки.
func (a *App) RunSchedules() {
	lastRun := map[string]string{}
	for {
		now := time.Now()
		keyDate := now.Format("2006-01-02")
		hm := now.Format("15:04")
		for _, s := range a.cfg.Schedules {
			if s.At == hm && lastRun[s.ID] != keyDate {
				lastRun[s.ID] = keyDate
				if s.Action == "" {
					continue
				}
				logInfo("schedule run: id=%s action=%s", s.ID, s.Action)
				if !a.RunNamedAction(s.Action, 0) {
					logError("schedule action failed: id=%s action=%s", s.ID, s.Action)
				}
			}
		}
		time.Sleep(time.Second)
	}
}

// lockFileSignature описывает конкретный экземпляр trigger-файла (время изменения и размер).
// Нужна, чтобы при delete_lock_file=false один и тот же файл не обрабатывался повторно
// на каждой итерации, а при неудачном удалении не было бесконечной повторной отправки.
func lockFileSignature(info os.FileInfo) string {
	return fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
}

// videoWatcher следит за trigger-файлом и вызывает process ровно один раз
// для каждого нового экземпляра файла.
type videoWatcher struct {
	lock          string
	deleteAfter   bool
	process       func(lock string)
	lastSignature string
	lastInfo      os.FileInfo
	lastStatErr   string
	lastClaimErr  string
}

// claimError журналирует ошибку забора trigger-файла один раз, а не каждую секунду
// (например, если у пользователя сервиса нет права записи в каталог trigger-файла).
func (w *videoWatcher) claimError(err error) {
	if err.Error() != w.lastClaimErr {
		logError("video trigger claim error (check write permission for %s): %v", filepath.Dir(w.lock), err)
		w.lastClaimErr = err.Error()
	}
}

// poll выполняет одну проверку trigger-файла. Возвращает true, если файл был обработан.
func (w *videoWatcher) poll() bool {
	info, err := os.Stat(w.lock)
	switch {
	case err == nil:
		w.lastStatErr = ""
		signature := lockFileSignature(info)
		if signature == w.lastSignature && w.lastInfo != nil && os.SameFile(info, w.lastInfo) {
			return false
		}
		if w.deleteAfter {
			// Атомарно забираем trigger ДО чтения и отправки видео. Новый файл
			// producer-а остаётся по исходному пути и обрабатывается следующим poll.
			claimed, err := os.CreateTemp(filepath.Dir(w.lock), ".smarthome-video-trigger-*")
			if err != nil {
				w.claimError(err)
				return false
			}
			name := claimed.Name()
			if err = claimed.Close(); err != nil {
				_ = os.Remove(name)
				w.claimError(err)
				return false
			}
			_ = os.Remove(name) // rename на Windows требует отсутствующего назначения
			if err = os.Rename(w.lock, name); err != nil {
				_ = os.Remove(name)
				w.claimError(err)
				return false
			}
			defer os.Remove(name)
			w.lastClaimErr = ""
			w.lastSignature = ""
			w.lastInfo = nil
			w.process(name)
		} else {
			w.lastSignature = signature
			w.lastInfo = info
			w.process(w.lock)
		}
		return true
	case errors.Is(err, os.ErrNotExist):
		w.lastSignature = ""
		w.lastInfo = nil
		w.lastStatErr = ""
	default:
		// Не засоряем журнал одной и той же ошибкой каждую секунду.
		if err.Error() != w.lastStatErr {
			logError("video lock file stat error %s: %v", w.lock, err)
			w.lastStatErr = err.Error()
		}
	}
	return false
}

// RunVideoMonitoring отслеживает trigger-файл видеомониторинга и отправляет видео
// пользователям, у которых включён видеоконтроль. Каждый экземпляр trigger-файла
// обрабатывается ровно один раз (раньше при delete_lock_file=false один и тот же
// ролик отправлялся заново на каждой итерации, т. е. каждую секунду).
func (a *App) RunVideoMonitoring() {
	if !a.cfg.VideoMonitoring.Enabled {
		return
	}
	w := &videoWatcher{
		lock:        a.cfg.VideoMonitoring.LockFile,
		deleteAfter: a.cfg.VideoMonitoring.DeleteLockFile,
		process:     a.processVideoTrigger,
	}
	for {
		w.poll()
		time.Sleep(a.cfg.VideoMonitoring.CheckInterval.Duration)
	}
}

// processVideoTrigger читает путь к видео из trigger-файла и отправляет ролик пользователям.
func (a *App) processVideoTrigger(lock string) {
	videoRef, err := os.ReadFile(lock)
	if err != nil {
		logError("video lock file read error %s: %v", lock, err)
		return
	}
	path := strings.TrimSpace(string(videoRef))
	if path == "" {
		logWarn("video lock file is empty: %s", lock)
		return
	}
	recipients := []int64{}
	for _, u := range a.cfg.Telegram.AllowedUsers {
		if a.userVideoEnabled(u.ID) {
			recipients = append(recipients, u.ID)
		}
	}
	if len(recipients) == 0 {
		logDebug("video skipped, no recipients: %s", path)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		logError("video read error %s: %v", path, err)
		return
	}
	if info.Size() > telegramMaxUploadBytes {
		logError("video too large for Telegram Bot API (%d bytes > %d): %s", info.Size(), telegramMaxUploadBytes, path)
		return
	}
	videoFile, err := os.ReadFile(path)
	if err != nil {
		logError("video read error %s: %v", path, err)
		return
	}
	for _, id := range recipients {
		_, err = a.bot.Send(tgbotapi.NewVideoUpload(id, tgbotapi.FileBytes{Name: filepath.Base(path), Bytes: videoFile}))
		if err != nil {
			logError("video send error: user=%d error=%v", id, a.telegramError(err))
		} else {
			logInfo("video sent: user=%d file=%s", id, filepath.Base(path))
		}
	}
}
