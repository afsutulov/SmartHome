package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// loadConfig читает JSON-конфигурацию из файла, проверяет обязательные поля и выставляет значения по умолчанию.
func loadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	var cfg Config
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if cfg.Telegram.Token == "" {
		return cfg, errors.New("telegram.token is required")
	}
	if cfg.MQTT.Server == "" {
		return cfg, errors.New("mqtt.server is required")
	}
	if cfg.Storage.StateFile == "" {
		cfg.Storage.StateFile = "/var/lib/smarthome/state.json"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.File == "" && !cfg.Logging.ToConsole {
		cfg.Logging.ToConsole = true
	}
	if cfg.Telegram.PollTimeoutSec == 0 {
		cfg.Telegram.PollTimeoutSec = 60
	}
	if cfg.Telegram.HTTPTimeoutSec == 0 {
		cfg.Telegram.HTTPTimeoutSec = 70
	}
	if cfg.Labels.Off == "" {
		cfg.Labels.Off = "ВЫКЛ"
	}
	if cfg.Labels.On == "" {
		cfg.Labels.On = "ВКЛ"
	}
	if cfg.Labels.No == "" {
		cfg.Labels.No = "НЕТ"
	}
	if cfg.Labels.Yes == "" {
		cfg.Labels.Yes = "ДА"
	}
	if cfg.Labels.Unknown == "" {
		cfg.Labels.Unknown = "Неизвестная команда!"
	}
	if cfg.Labels.DateTime == "" {
		cfg.Labels.DateTime = "02.01.06 15:04"
	}
	if cfg.VideoMonitoring.CheckInterval.Duration == 0 {
		cfg.VideoMonitoring.CheckInterval.Duration = time.Second
	}
	if cfg.Menu.ButtonWidth == 0 {
		cfg.Menu.ButtonWidth = 20
	}
	for i := range cfg.CommandIntegrations {
		if len(cfg.CommandIntegrations[i].Topics) == 0 && cfg.CommandIntegrations[i].Topic != "" {
			cfg.CommandIntegrations[i].Topics = []string{cfg.CommandIntegrations[i].Topic}
		}
		if cfg.CommandIntegrations[i].Topic == "" && len(cfg.CommandIntegrations[i].Topics) > 0 {
			cfg.CommandIntegrations[i].Topic = cfg.CommandIntegrations[i].Topics[0]
		}
		if cfg.CommandIntegrations[i].ActionOn == "" {
			cfg.CommandIntegrations[i].ActionOn = "on"
		}
		if cfg.CommandIntegrations[i].ActionOff == "" {
			cfg.CommandIntegrations[i].ActionOff = "off"
		}
		if len(cfg.CommandIntegrations[i].PayloadOn) == 0 {
			cfg.CommandIntegrations[i].PayloadOn = []string{"true", "on", "1", "вкл"}
		}
		if len(cfg.CommandIntegrations[i].PayloadOff) == 0 {
			cfg.CommandIntegrations[i].PayloadOff = []string{"false", "off", "0", "выкл"}
		}
	}
	return cfg, nil
}

// ValidateConfig проверяет ссылки в конфигурации: меню, устройства, группы, расписания и действия.
func ValidateConfig(cfg Config) error {
	devices := map[string]DeviceConfig{}
	groups := map[string]GroupConfig{}
	for _, d := range cfg.Devices {
		if d.ID == "" {
			return errors.New("device id is required")
		}
		if _, exists := devices[d.ID]; exists {
			return fmt.Errorf("duplicate device id %q", d.ID)
		}
		devices[d.ID] = d
	}
	for _, g := range cfg.Groups {
		if g.ID == "" {
			return errors.New("group id is required")
		}
		if _, exists := groups[g.ID]; exists {
			return fmt.Errorf("duplicate group id %q", g.ID)
		}
		for _, member := range g.Members {
			if _, ok := devices[member]; !ok {
				return fmt.Errorf("group %q references unknown device %q", g.ID, member)
			}
		}
		groups[g.ID] = g
	}
	for _, ci := range cfg.CommandIntegrations {
		if !ci.Enabled {
			continue
		}
		if ci.ID == "" {
			return errors.New("command integration id is required")
		}
		if ci.Topic == "" && len(ci.Topics) == 0 {
			return fmt.Errorf("command integration %q topic/topics is required", ci.ID)
		}
	}
	for rowIdx, row := range cfg.Menu.Rows {
		for itemIdx, item := range row {
			if err := validateMenuItem(item, devices, groups); err != nil {
				return fmt.Errorf("menu row %d item %d: %w", rowIdx+1, itemIdx+1, err)
			}
		}
	}
	for _, s := range cfg.Schedules {
		if s.Action != "" {
			if err := validateNamedAction(s.Action, devices, groups); err != nil {
				return fmt.Errorf("schedule %q: %w", s.ID, err)
			}
		}
	}
	return nil
}

// validateMenuItem проверяет один пункт Telegram-меню.
func validateMenuItem(item MenuItem, devices map[string]DeviceConfig, groups map[string]GroupConfig) error {
	if item.Builtin != "" {
		switch item.Builtin {
		case "status", "alarms", "batteries":
			return nil
		default:
			return fmt.Errorf("unknown builtin %q", item.Builtin)
		}
	}
	if item.DeviceID != "" {
		d, ok := devices[item.DeviceID]
		if !ok {
			return fmt.Errorf("unknown device_id %q", item.DeviceID)
		}
		if _, ok := d.Actions[item.Action]; !ok {
			return fmt.Errorf("device %q has no action %q", item.DeviceID, item.Action)
		}
		return nil
	}
	if item.GroupID != "" {
		g, ok := groups[item.GroupID]
		if !ok {
			return fmt.Errorf("unknown group_id %q", item.GroupID)
		}
		if _, ok := g.Actions[item.Action]; !ok && item.Action == "" {
			return fmt.Errorf("group %q action is empty", item.GroupID)
		}
		return nil
	}
	return errors.New("item must contain builtin, device_id or group_id")
}

// validateNamedAction проверяет строку действия вида "device.action" или "group.action".
func validateNamedAction(name string, devices map[string]DeviceConfig, groups map[string]GroupConfig) error {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid action %q, expected object.action", name)
	}
	if d, ok := devices[parts[0]]; ok {
		if _, ok := d.Actions[parts[1]]; !ok {
			return fmt.Errorf("device %q has no action %q", parts[0], parts[1])
		}
		return nil
	}
	if g, ok := groups[parts[0]]; ok {
		if _, ok := g.Actions[parts[1]]; !ok {
			return fmt.Errorf("group %q has no action %q", parts[0], parts[1])
		}
		return nil
	}
	return fmt.Errorf("unknown action target %q", parts[0])
}
