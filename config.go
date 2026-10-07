package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	if err := dec.Decode(new(any)); err != io.EOF {
		return Config{}, errors.New("config must contain exactly one JSON object")
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
	for _, target := range []string{cfg.Storage.StateFile, cfg.Logging.File} {
		if target != "" && pathsAlias(path, target) {
			return cfg, errors.New("storage/logging file must not overwrite config")
		}
	}
	if cfg.Logging.File != "" && pathsAlias(cfg.Storage.StateFile, cfg.Logging.File) {
		return cfg, errors.New("storage.state_file and logging.file must differ")
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
	switch strings.ToLower(cfg.Logging.Level) {
	case "", "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("unknown logging.level %q", cfg.Logging.Level)
	}
	if cfg.MQTT.QOS > 2 {
		return errors.New("mqtt.qos must be 0, 1 or 2")
	}
	if cfg.Telegram.PollTimeoutSec < 0 || cfg.Telegram.HTTPTimeoutSec <= cfg.Telegram.PollTimeoutSec {
		return errors.New("telegram.http_timeout_sec must exceed nonnegative poll_timeout_sec")
	}
	if cfg.VideoMonitoring.CheckInterval.Duration <= 0 {
		return errors.New("video_monitoring.check_interval must be positive")
	}
	if cfg.VideoMonitoring.Enabled && strings.TrimSpace(cfg.VideoMonitoring.LockFile) == "" {
		return errors.New("video_monitoring.lock_file is required when video_monitoring.enabled is true")
	}
	if pc := cfg.Telegram.Proxy; pc.Enabled && !strings.EqualFold(pc.Type, "none") {
		if !strings.EqualFold(pc.Type, "socks5") {
			return fmt.Errorf("telegram.proxy.type %q is not supported (use \"socks5\" or \"none\")", pc.Type)
		}
		if strings.TrimSpace(pc.Address) == "" {
			return errors.New("telegram.proxy.address is required for socks5 proxy")
		}
	}
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
		for _, r := range d.Reports {
			if r.Expression == "" && r.Field == "" {
				return fmt.Errorf("device %q: report %q needs expression or field", d.ID, r.Name)
			}
			if r.Expression != "" {
				if err := validateExpression(r.Expression); err != nil {
					return fmt.Errorf("device %q: report %q: %w", d.ID, r.Name, err)
				}
			}
		}
	}
	for _, g := range cfg.Groups {
		if g.ID == "" {
			return errors.New("group id is required")
		}
		if _, exists := groups[g.ID]; exists {
			return fmt.Errorf("duplicate group id %q", g.ID)
		}
		if _, exists := devices[g.ID]; exists {
			return fmt.Errorf("group and device share id %q", g.ID)
		}
		for _, member := range g.Members {
			if _, ok := devices[member]; !ok {
				return fmt.Errorf("group %q references unknown device %q", g.ID, member)
			}
		}
		groups[g.ID] = g
	}
	// Проверяем все цепочки, включая аварийные правила, а не только меню.
	graph := map[string][]string{}
	guardTypes := map[string]any{}
	checkAction := func(ref string, act ActionConfig) error {
		if act.SetAllUsersVideo != nil && (act.SetUserVideo != nil || act.VideoUserID != 0) {
			return fmt.Errorf("action %s: set_all_users_video cannot be combined with personal video settings", ref)
		}
		if act.VideoUserID != 0 {
			if _, isGroup := groups[strings.SplitN(ref, ".", 2)[0]]; isGroup {
				return fmt.Errorf("action %s: video_user_id is supported in device actions only", ref)
			}
			if act.SetUserVideo == nil {
				return fmt.Errorf("action %s: video_user_id requires set_user_video", ref)
			}
			allowed := false
			for _, user := range cfg.Telegram.AllowedUsers {
				if user.ID > 0 && user.ID == act.VideoUserID {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("action %s: video_user_id must reference a positive telegram.allowed_users id", ref)
			}
		}
		if act.Notify != nil {
			if act.Notify.Users != "" && act.Notify.Users != "all" && act.Notify.Users != "video_enabled" {
				return fmt.Errorf("action %s: unknown notify.users", ref)
			}
			if strings.TrimSpace(act.Notify.Text) == "" {
				return fmt.Errorf("action %s: notify.text must not be empty", ref)
			}
		}
		for _, guard := range act.BlockWhen {
			if _, ok := devices[guard.DeviceID]; !ok {
				return fmt.Errorf("action %s: unknown block_when device %q", ref, guard.DeviceID)
			}
			if strings.TrimSpace(guard.Field) == "" || guard.Value == nil {
				return fmt.Errorf("action %s: block_when needs field and scalar value", ref)
			}
			switch guard.Value.(type) {
			case bool, string, float64:
			default:
				return fmt.Errorf("action %s: block_when value must be bool, string or number", ref)
			}
			key := guard.DeviceID + "\x00" + guard.Field
			if previous, exists := guardTypes[key]; exists && !sameGuardType(guard.Value, previous) {
				return fmt.Errorf("action %s: incompatible block_when types for %s.%s", ref, guard.DeviceID, guard.Field)
			}
			guardTypes[key] = guard.Value
		}
		for _, next := range act.RunActions {
			if err := validateNamedAction(next, devices, groups); err != nil {
				return fmt.Errorf("action %s: %w", ref, err)
			}
		}
		graph[ref] = act.RunActions
		return nil
	}
	for _, d := range cfg.Devices {
		for name, act := range d.Actions {
			if err := checkAction(d.ID+"."+name, act); err != nil {
				return err
			}
		}
	}
	for _, g := range cfg.Groups {
		for name, act := range g.Actions {
			if err := checkAction(g.ID+"."+name, act); err != nil {
				return err
			}
		}
	}
	// Неявные действия групп также участвуют в проверке циклов.
	for _, g := range cfg.Groups {
		for _, d := range cfg.Devices {
			for action := range d.Actions {
				ref := g.ID + "." + action
				if _, explicit := g.Actions[action]; !explicit {
					for _, member := range g.Members {
						graph[ref] = append(graph[ref], member+"."+action)
					}
				}
			}
		}
	}
	if err := validateActionGraph(graph); err != nil {
		return err
	}
	eventIDs := map[string]bool{}
	for _, ev := range cfg.Events {
		if ev.ID == "" || eventIDs[ev.ID] {
			return fmt.Errorf("event id empty or duplicated: %q", ev.ID)
		}
		eventIDs[ev.ID] = true
		if ev.DeviceID != "" {
			if _, ok := devices[ev.DeviceID]; !ok {
				return fmt.Errorf("event %q: unknown device %q", ev.ID, ev.DeviceID)
			}
		}
		if len(ev.Updates) > 0 && ev.DeviceID == "" {
			return fmt.Errorf("event %q: updates require device_id", ev.ID)
		}
		for _, ref := range ev.Actions {
			if err := validateNamedAction(ref, devices, groups); err != nil {
				return fmt.Errorf("event %q: %w", ev.ID, err)
			}
		}
		for field, expr := range ev.Updates {
			if err := validateExpression(expr); err != nil {
				return fmt.Errorf("event %q: updates.%s: %w", ev.ID, field, err)
			}
		}
		if ev.Notify != nil && ev.Notify.Users != "all" && ev.Notify.Users != "video_enabled" && ev.Notify.Users != "" {
			return fmt.Errorf("event %q: unknown notify.users", ev.ID)
		}
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
	scheduleIDs := map[string]bool{}
	for _, s := range cfg.Schedules {
		if s.ID == "" || scheduleIDs[s.ID] {
			return fmt.Errorf("schedule id empty or duplicated: %q", s.ID)
		}
		scheduleIDs[s.ID] = true
		if _, err := time.Parse("15:04", s.At); err != nil {
			return fmt.Errorf("schedule %q: invalid at %q", s.ID, s.At)
		}
		if s.Action != "" {
			if err := validateNamedAction(s.Action, devices, groups); err != nil {
				return fmt.Errorf("schedule %q: %w", s.ID, err)
			}
		}
	}
	return validateConfigTemplates(cfg, devices)
}

func pathsAlias(first, second string) bool {
	a, _ := filepath.Abs(first)
	b, _ := filepath.Abs(second)
	if a == b {
		return true
	}
	aInfo, aErr := os.Stat(first)
	bInfo, bErr := os.Stat(second)
	return aErr == nil && bErr == nil && os.SameFile(aInfo, bInfo)
}

// Топики должны полностью рендериться при запуске. Payload/уведомление может ссылаться
// на динамические поля: ошибки их рендеринга журналируются при сообщении.
func validateConfigTemplates(cfg Config, devices map[string]DeviceConfig) error {
	check := func(t string, data map[string]any) error {
		if _, err := renderChecked(t, data); err != nil {
			return fmt.Errorf("invalid topic template %q: %w", t, err)
		}
		return nil
	}
	base := map[string]any{"BaseTopic": cfg.MQTT.BaseTopics}
	for _, topic := range cfg.MQTT.SubscribeTo {
		if err := check(topic, base); err != nil {
			return err
		}
	}
	for _, d := range cfg.Devices {
		ctx := map[string]any{"Device": d, "BaseTopic": cfg.MQTT.BaseTopics}
		for _, topic := range d.Subscribe {
			if err := check(topic, ctx); err != nil {
				return err
			}
		}
		for _, act := range d.Actions {
			for _, p := range act.Publishes {
				if err := check(p.Topic, ctx); err != nil {
					return err
				}
			}
		}
		for _, r := range d.Reports {
			if err := check(r.Topic, ctx); err != nil {
				return err
			}
		}
		for _, ci := range cfg.CommandIntegrations {
			if ci.Enabled {
				for _, topic := range commandIntegrationTopics(ci) {
					if topicTemplateApplies(d, topic) {
						if err := check(topic, ctx); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	for _, g := range cfg.Groups {
		ctx := map[string]any{"Group": g, "BaseTopic": cfg.MQTT.BaseTopics}
		for _, act := range g.Actions {
			for _, p := range act.Publishes {
				if err := check(p.Topic, ctx); err != nil {
					return err
				}
			}
		}
	}
	for _, ev := range cfg.Events {
		ctx := map[string]any{"Device": devices[ev.DeviceID], "BaseTopic": cfg.MQTT.BaseTopics}
		if err := check(ev.OnTopic, ctx); err != nil {
			return err
		}
		for _, p := range ev.Publishes {
			if err := check(p.Topic, ctx); err != nil {
				return err
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
		if item.Action == "" {
			return fmt.Errorf("group %q action is empty", item.GroupID)
		}
		return validateNamedAction(g.ID+"."+item.Action, devices, groups)
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
			if len(g.Members) == 0 {
				return fmt.Errorf("group %q has no action %q", parts[0], parts[1])
			}
			for _, id := range g.Members {
				if _, ok := devices[id].Actions[parts[1]]; !ok {
					return fmt.Errorf("group %q member %q has no action %q", g.ID, id, parts[1])
				}
			}
		}
		return nil
	}
	return fmt.Errorf("unknown action target %q", parts[0])
}

func validateActionGraph(graph map[string][]string) error {
	status := map[string]int{}
	var visit func(string) error
	visit = func(ref string) error {
		if status[ref] == 1 {
			return fmt.Errorf("cyclic action chain at %q", ref)
		}
		if status[ref] == 2 {
			return nil
		}
		status[ref] = 1
		for _, next := range graph[ref] {
			if err := visit(next); err != nil {
				return err
			}
		}
		status[ref] = 2
		return nil
	}
	for ref := range graph {
		if err := visit(ref); err != nil {
			return err
		}
	}
	return nil
}

// validateExpression проверяет выражение из updates/reports заранее, при запуске.
// Раньше опечатка (например, "eq(payload.state 'ON')") молча возвращалась как строковая
// константа и записывалась в состояние. Строки без признаков выражения (без скобок,
// без "$" и без "payload") по-прежнему трактуются как константы.
func validateExpression(expr string) error {
	e := strings.TrimSpace(expr)
	if e != expr {
		return fmt.Errorf("expression %q has leading/trailing spaces", expr)
	}
	isExpression := looksLikeExpression(e)
	if !isExpression {
		return nil
	}
	if _, err := checkExpression(e); err != nil {
		return fmt.Errorf("invalid expression %q: %w", expr, err)
	}
	return nil
}

// checkExpression разбирает выражение по тем же правилам, что evalExpression,
// и сообщает, возвращает ли оно булево значение.
func checkExpression(e string) (bool, error) {
	field := func(s string) error {
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "payload.") || len(s) == len("payload.") || strings.ContainsAny(strings.TrimPrefix(s, "payload."), "(), \t\n\r\"'") {
			return fmt.Errorf("expected payload.<field>, got %q", s)
		}
		return nil
	}
	if e == "$raw" {
		return false, nil
	}
	if e == "$raw_bool" {
		return true, nil
	}
	if strings.HasPrefix(e, "payload.") {
		return false, field(e)
	}
	name, args, err := parseExpressionCall(e)
	if err != nil {
		return false, err
	}
	switch name {
	case "eq", "ne":
		if len(args) != 2 {
			return false, fmt.Errorf("%s() needs two arguments", name)
		}
		if err := field(args[0]); err != nil {
			return false, err
		}
		if _, err := expressionLiteral(args[1]); err != nil {
			return false, err
		}
		return true, nil
	case "and":
		if len(args) < 2 {
			return false, errors.New("and() needs at least two arguments")
		}
		for _, arg := range args {
			isBool, err := checkExpression(strings.TrimSpace(arg))
			if err != nil {
				return false, err
			}
			if !isBool {
				return false, fmt.Errorf("and() argument %q is not boolean", arg)
			}
		}
		return true, nil
	case "pressure_mmhg":
		if len(args) != 1 {
			return false, errors.New("pressure_mmhg() needs one argument")
		}
		return false, field(args[0])
	}
	return false, fmt.Errorf("unknown expression function %q", name)
}
