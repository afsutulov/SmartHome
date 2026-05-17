package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RunSchedules каждую секунду проверяет расписание и запускает нужные действия один раз в сутки.
func (a *App) RunSchedules() {
	lastRun := map[string]string{}
	for {
		now := time.Now()
		keyDate := now.Format("2006-01-02")
		hm := now.Format("15:04")
		for _, s := range a.cfg.Schedules {
			if s.At == hm && lastRun[s.ID] != keyDate {
				a.RunNamedAction(s.Action, 0)
				lastRun[s.ID] = keyDate
			}
		}
		time.Sleep(time.Second)
	}
}

// RunVideoMonitoring отслеживает trigger-файл видеомониторинга и отправляет видео пользователям у которых включен Видеомонииторинг.
func (a *App) RunVideoMonitoring() {
	if !a.cfg.VideoMonitoring.Enabled {
		return
	}
	for {
		lock := a.cfg.VideoMonitoring.LockFile
		videoRef, err := os.ReadFile(lock)
		if err == nil {
			path := strings.TrimSpace(string(videoRef))
			videoFile, err := os.ReadFile(path)
			if err == nil {
				for _, u := range a.cfg.Telegram.AllowedUsers {
					if a.userVideoEnabled(u.ID) {
						_, err = a.bot.Send(tgbotapi.NewVideoUpload(u.ID, tgbotapi.FileBytes{Name: filepath.Base(path), Bytes: videoFile}))
						if err != nil {
							logError("video send error: %v", err)
						}
					}
				}
			} else {
				logError("video read error %s: %v", path, err)
			}
			if a.cfg.VideoMonitoring.DeleteLockFile {
				_ = os.Remove(lock)
			}
		}
		time.Sleep(a.cfg.VideoMonitoring.CheckInterval.Duration)
	}
}
