package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"io"
	"os"
	"path/filepath"
)

var errStateRecovered = errors.New("corrupt state preserved; defaults used")

// NewApp создаёт экземпляр приложения, строит индексы устройств, групп, пользователей и меню.
func NewApp(cfg Config) *App {
	a := &App{cfg: cfg, devices: map[string]DeviceConfig{}, groups: map[string]GroupConfig{}, users: map[int64]UserConfig{}, menuIndex: map[string]MenuItem{}}
	a.runtime = map[string]*deviceRuntime{}
	a.stateVersions = map[string]uint64{}
	a.eventActive = map[int]bool{}
	a.reportedPayloads = map[string]map[string]any{}
	a.reportFields = map[string]map[string]bool{}
	a.mqttInbox = make(chan mqttTask, 1024)
	a.notifications = make(chan tgbotapi.MessageConfig, 256)
	for _, d := range cfg.Devices {
		a.devices[d.ID] = d
		a.runtime[d.ID] = &deviceRuntime{}
		a.reportFields[d.ID] = map[string]bool{}
		for _, r := range d.Reports {
			for _, field := range expressionFields(r.Expression) {
				a.reportFields[d.ID][field] = true
			}
			if r.Field != "" {
				a.reportFields[d.ID][r.Field] = true
			}
		}
	}
	for _, ev := range cfg.Events {
		for _, expr := range ev.Updates {
			for _, field := range expressionFields(expr) {
				if a.reportFields[ev.DeviceID] == nil {
					continue
				}
				a.reportFields[ev.DeviceID][field] = true
			}
		}
	}
	for _, g := range cfg.Groups {
		a.groups[g.ID] = g
	}
	for _, u := range cfg.Telegram.AllowedUsers {
		a.users[u.ID] = u
	}
	for _, row := range cfg.Menu.Rows {
		for _, item := range row {
			text := normalizeTelegramText(a.RenderMenuText(item))
			if _, exists := a.menuIndex[text]; exists {
				logWarn("duplicate telegram menu button text: %q", text)
			}
			a.menuIndex[text] = item
		}
	}
	a.state.Devices = map[string]map[string]any{}
	a.state.Users = map[string]UserState{}
	for _, d := range cfg.Devices {
		a.state.Devices[d.ID] = cloneMap(d.Initial)
	}
	a.ensureStateDefaults()
	a.initChainLocks()
	a.initGuardTypes()
	for _, u := range cfg.Telegram.AllowedUsers {
		a.state.Users[fmt.Sprint(u.ID)] = UserState{VideoMonitoringEnabled: u.VideoMonitoringEnable}
	}
	return a
}

// cloneMap создаёт поверхностную копию map, чтобы безопасно возвращать состояние без прямой ссылки.
func cloneMap(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// LoadState загружает сохранённое runtime-состояние устройств и пользователей из файла.
func (a *App) LoadState() error {
	a.invalidateGuardKnowledge()
	f, err := os.Open(a.cfg.Storage.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		logInfo("state file does not exist yet, defaults will be used: %s", a.cfg.Storage.StateFile)
		a.stateMu.Lock()
		a.ensureStateDefaults()
		a.stateMu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	raw, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		logWarn("state file is empty, defaults will be used: %s", a.cfg.Storage.StateFile)
		a.stateMu.Lock()
		a.ensureStateDefaults()
		a.stateMu.Unlock()
		return nil
	}

	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		a.stateMu.Lock()
		a.ensureStateDefaults()
		a.stateMu.Unlock()
		// Без резервной копии первый же SaveState затёр бы повреждённый файл,
		// и восстановить из него данные было бы уже невозможно.
		backup, backupErr := os.CreateTemp(filepath.Dir(a.cfg.Storage.StateFile), filepath.Base(a.cfg.Storage.StateFile)+".corrupt-*")
		if backupErr != nil {
			return fmt.Errorf("state file has invalid format: %w; backup failed: %v", err, backupErr)
		}
		if _, backupErr = backup.Write(raw); backupErr == nil {
			backupErr = backup.Sync()
		}
		if closeErr := backup.Close(); backupErr == nil {
			backupErr = closeErr
		}
		if backupErr != nil {
			_ = os.Remove(backup.Name())
			return fmt.Errorf("state file has invalid format: %w; backup failed: %v", err, backupErr)
		}
		return fmt.Errorf("%w: %v; copy at %s", errStateRecovered, err, backup.Name())
	}
	if s.Devices == nil {
		s.Devices = map[string]map[string]any{}
	}
	if s.Users == nil {
		s.Users = map[string]UserState{}
	}

	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	for id, values := range s.Devices {
		if _, ok := a.state.Devices[id]; !ok {
			a.state.Devices[id] = map[string]any{}
		}
		for k, v := range values {
			a.state.Devices[id][k] = v
			a.acceptGuardValue(id, k, v)
		}
	}
	for id, st := range s.Users {
		a.state.Users[id] = st
	}
	a.ensureStateDefaults()
	logInfo("state loaded: %s", a.cfg.Storage.StateFile)
	return nil
}

// ensureStateDefaults добавляет отсутствующие записи состояния и выставляет last_seen по умолчанию.
func (a *App) ensureStateDefaults() {
	for _, d := range a.cfg.Devices {
		if _, ok := a.state.Devices[d.ID]; !ok {
			a.state.Devices[d.ID] = map[string]any{}
		}
		if _, ok := a.state.Devices[d.ID]["last_seen"]; !ok {
			a.state.Devices[d.ID]["last_seen"] = defaultLastSeen
		}
	}
}

// SaveState сохраняет текущее runtime-состояние в JSON-файл с читаемым UTF-8.
func (a *App) SaveState() {
	// Снимок и замена файла должны выполняться в одном порядке для всех писателей.
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	if a.statePersistenceDisabled {
		return
	}
	a.stateMu.RLock()
	// Do not turn unknown/default sensor values into trusted saved values.
	snapshot := State{Devices: map[string]map[string]any{}, Users: a.state.Users}
	for id, fields := range a.state.Devices {
		snapshot.Devices[id] = cloneMap(fields)
		for field := range a.unknownGuards[id] {
			delete(snapshot.Devices[id], field)
		}
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	a.stateMu.RUnlock()
	if err != nil {
		logError("state encode error: %v", err)
		return
	}

	if err := os.MkdirAll(filepath.Dir(a.cfg.Storage.StateFile), 0755); err != nil {
		logError("state dir error: %v", err)
		return
	}

	file, err := os.CreateTemp(filepath.Dir(a.cfg.Storage.StateFile), ".smarthome-state-*")
	if err != nil {
		logError("state open error: %v", err)
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), a.cfg.Storage.StateFile)
	}
	if err != nil {
		logError("state save error: %v", err)
	}
}
