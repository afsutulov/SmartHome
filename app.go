package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// NewApp создаёт экземпляр приложения, строит индексы устройств, групп, пользователей и меню.
func NewApp(cfg Config) *App {
	a := &App{cfg: cfg, devices: map[string]DeviceConfig{}, groups: map[string]GroupConfig{}, users: map[int64]UserConfig{}, menuIndex: map[string]MenuItem{}}
	for _, d := range cfg.Devices {
		a.devices[d.ID] = d
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
		return fmt.Errorf("state file has invalid format: %w; remove or recreate %s", err, a.cfg.Storage.StateFile)
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
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(a.cfg.Storage.StateFile), 0755); err != nil {
		logError("state dir error: %v", err)
		return
	}

	file, err := os.OpenFile(a.cfg.Storage.StateFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		logError("state open error: %v", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(a.state); err != nil {
		logError("state encode error: %v", err)
	}
}
