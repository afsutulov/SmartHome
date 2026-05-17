package main

import (
	"encoding/json"
	"fmt"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"math"
	"strconv"
	"strings"
	"time"
)

// ConnectMQTT создаёт MQTT-клиент, подключается к брокеру и настраивает обработчик входящих сообщений.
func (a *App) ConnectMQTT() mqtt.Client {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(a.cfg.MQTT.Server)
	opts.SetUsername(a.cfg.MQTT.Login)
	opts.SetPassword(a.cfg.MQTT.Password)
	if a.cfg.MQTT.ClientID == "" {
		a.cfg.MQTT.ClientID = "SmartHome"
	}
	opts.SetClientID(a.cfg.MQTT.ClientID)
	opts.SetDefaultPublishHandler(a.OnMQTTMessage)
	opts.SetKeepAlive(0)
	opts.SetOrderMatters(false)
	opts.SetAutoReconnect(true)
	opts.SetConnectionLostHandler(func(client mqtt.Client, err error) { logWarn("mqtt connection lost: %v", err) })
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		logError("mqtt connection error: %v", token.Error())
	}
	return client
}

// SubscribeMQTT подписывает MQTT-клиент на топики из конфигурации, устройств и событий.
func (a *App) SubscribeMQTT() {
	seen := map[string]bool{}
	for _, t := range a.cfg.MQTT.SubscribeTo {
		a.subscribe(render(t, map[string]any{}), seen)
	}
	for _, d := range a.cfg.Devices {
		for _, t := range d.Subscribe {
			a.subscribe(a.RenderWithDevice(t, d, nil), seen)
		}
		if d.ZigbeeID != "" {
			a.subscribe(fmt.Sprintf("%s/%s", a.baseTopic(0), d.ZigbeeID), seen)
		}
	}
	for _, ci := range a.cfg.CommandIntegrations {
		if !ci.Enabled {
			continue
		}
		for _, d := range a.cfg.Devices {
			if d.Type == "virtual" {
				continue
			}
			for _, topicTpl := range commandIntegrationTopics(ci) {
				a.subscribe(a.RenderWithDevice(topicTpl, d, nil), seen)
			}
		}
	}
	for _, ev := range a.cfg.Events {
		if ev.OnTopic != "" {
			a.subscribe(a.RenderTopic(ev.OnTopic, ev.DeviceID), seen)
		}
	}
}

// subscribe выполняет подписку на один MQTT-topic и не допускает повторных подписок.
func (a *App) subscribe(topic string, seen map[string]bool) {
	if topic == "" || seen[topic] {
		return
	}
	seen[topic] = true
	logDebug("mqtt subscribe: topic=%s", topic)
	if token := a.mqtt.Subscribe(topic, a.cfg.MQTT.QOS, nil); token.Wait() && token.Error() != nil {
		logError("subscribe %s error: %v", topic, token.Error())
	}
}

// Publish отправляет payload в MQTT-topic с QoS и retained из конфигурации.
func (a *App) Publish(topic, payload string) {
	if topic == "" {
		return
	}
	if token := a.mqtt.Publish(topic, a.cfg.MQTT.QOS, a.cfg.MQTT.Retained, payload); token.Wait() && token.Error() != nil {
		logError("mqtt publish %s error: %v", topic, token.Error())
	}
}

// OnMQTTMessage обрабатывает входящее MQTT-сообщение и запускает подходящие события из конфигурации.
func (a *App) OnMQTTMessage(client mqtt.Client, msg mqtt.Message) {
	payload := map[string]any{}
	_ = json.Unmarshal(msg.Payload(), &payload)
	raw := strings.TrimSpace(string(msg.Payload()))
	logDebug("mqtt incoming: topic=%s payload=%q", msg.Topic(), raw)
	if a.isCommandIntegrationCandidate(msg.Topic()) {
		logInfo("command integration candidate: topic=%s payload=%q", msg.Topic(), raw)
	}
	if a.HandleCommandIntegration(msg.Topic(), raw) {
		return
	}
	touched := false
	handled := false
	if deviceID := a.DeviceIDByTopic(msg.Topic()); deviceID != "" {
		a.TouchDevice(deviceID)
		touched = true
		logDebug("mqtt message matched device: topic=%s device=%s", msg.Topic(), deviceID)
	}
	ctx := map[string]any{"Topic": msg.Topic(), "Payload": payload, "RawPayload": raw, "BoolPayload": isTruePayload(raw)}
	for _, ev := range a.cfg.Events {
		if ev.OnTopic != "" && !topicMatch(a.RenderTopic(ev.OnTopic, ev.DeviceID), msg.Topic()) {
			continue
		}
		handled = true
		if ev.DeviceID != "" {
			if d, ok := a.devices[ev.DeviceID]; ok && d.ZigbeeID != "" && !strings.Contains(msg.Topic(), d.ZigbeeID) && !strings.Contains(msg.Topic(), d.YandexID) {
				continue
			}
		}
		prevState := map[string]any{}
		if ev.DeviceID != "" {
			prevState = a.getDeviceState(ev.DeviceID)
		}
		updated := false
		if ev.DeviceID != "" {
			updated = a.ApplyUpdates(ev.DeviceID, ev.Updates, payload, raw)
		}
		if !whenMatches(ev.When, payload, raw) {
			if updated {
				a.SaveState()
			}
			continue
		}
		if updated {
			a.LogImportantStateChange(ev, prevState)
		}
		if isRepeatedTrueEvent(ev, prevState) {
			logDebug("skip repeated true event: event=%s device=%s", ev.ID, ev.DeviceID)
			if updated {
				a.SaveState()
			}
			continue
		}
		if ev.DeviceID != "" {
			if d, ok := a.devices[ev.DeviceID]; ok {
				for _, r := range d.Reports {
					value := evalReport(r, payload, raw)
					if value == nil {
						logDebug("report skipped: device=%s topic=%s expression=%q reason=nil", d.ID, a.RenderWithDevice(r.Topic, d, payload), r.Expression)
						continue
					}
					a.Publish(a.RenderWithDevice(r.Topic, d, payload), fmt.Sprint(value))
				}
			}
		}
		for _, p := range ev.Publishes {
			a.Publish(a.RenderTopic(p.Topic, ev.DeviceID), renderPayload(p.Payload, ctx))
		}
		for _, ref := range ev.Actions {
			a.RunNamedAction(ref, 0)
		}
		if ev.Notify != nil {
			a.Notify(ev.Notify, payload, raw)
		}
		a.SaveState()
	}
	if touched && !handled {
		a.SaveState()
	}
}

// HandleCommandIntegration обрабатывает команды внешних MQTT-интеграций, заданных в config.
// Возвращает true если сообщение было обработано как команда интеграции.
// При этом передаёт флаг fromIntegration=true чтобы предотвратить повторную публикацию
// в топик интеграции из действия устройства (защита от петли).
func (a *App) HandleCommandIntegration(topic string, raw string) bool {
	for _, ci := range a.cfg.CommandIntegrations {
		if !ci.Enabled {
			continue
		}
		for _, d := range a.cfg.Devices {
			if d.Type == "virtual" || !deviceHasCommandTopicData(d, ci) {
				continue
			}
			for _, topicTpl := range commandIntegrationTopics(ci) {
				renderedTopic := a.RenderWithDevice(topicTpl, d, nil)
				if !sameMQTTTopic(topic, renderedTopic) {
					continue
				}
				action, ok := commandAction(ci, raw)
				if !ok {
					logWarn("command integration ignored: integration=%s topic=%s unsupported payload=%q", ci.ID, topic, raw)
					return true
				}
				logInfo("command integration: integration=%s device=%s action=%s payload=%q force=%v", ci.ID, d.ID, action, raw, ci.Force)
				// Команда пришла из внешней интеграции, поэтому:
				// 1) можно принудительно выполнить действие, даже если локальный state устарел;
				// 2) нельзя публиковать обратно в set-topic этой же интеграции.
				a.RunDeviceActionFromIntegration(d.ID, action, ci.ID, ci.Force)
				return true
			}
		}
	}
	return false
}

// isCommandIntegrationCandidate проверяет, похож ли topic на одну из command integrations.
// Используется только для диагностического логирования. Пустой prefix пропускаем —
// иначе каждое MQTT-сообщение будет помечаться как кандидат (strings.HasPrefix(s,"")=true).
func (a *App) isCommandIntegrationCandidate(topic string) bool {
	for _, ci := range a.cfg.CommandIntegrations {
		if !ci.Enabled {
			continue
		}
		for _, topicTpl := range commandIntegrationTopics(ci) {
			prefix := commandTopicPrefix(topicTpl)
			normalizedPrefix := normalizeMQTTTopic(prefix)
			if normalizedPrefix == "" {
				continue // пустой prefix совпал бы со всем — пропускаем
			}
			if strings.HasPrefix(normalizeMQTTTopic(topic), normalizedPrefix) {
				return true
			}
		}
	}
	return false
}

// commandTopicPrefix возвращает постоянную часть topic-шаблона до первой template-вставки.
func commandTopicPrefix(topicTpl string) string {
	idx := strings.Index(topicTpl, "{{")
	if idx < 0 {
		return topicTpl
	}
	return topicTpl[:idx]
}

// deviceHasCommandTopicData проверяет, есть ли у устройства данные для template topic.
func deviceHasCommandTopicData(d DeviceConfig, ci CommandIntegrationConfig) bool {
	for _, topicTpl := range commandIntegrationTopics(ci) {
		if strings.Contains(topicTpl, ".Device.YandexID") && d.YandexID == "" {
			continue
		}
		return true
	}
	return false
}

// sameMQTTTopic сравнивает MQTT topic, игнорируя лишний ведущий slash.
func sameMQTTTopic(a string, b string) bool {
	return normalizeMQTTTopic(a) == normalizeMQTTTopic(b)
}

// normalizeMQTTTopic нормализует topic для сопоставления внешних интеграций.
func normalizeMQTTTopic(topic string) string {
	return strings.Trim(topic, "/")
}

// commandIntegrationTopics возвращает список topic-шаблонов интеграции.
func commandIntegrationTopics(ci CommandIntegrationConfig) []string {
	if len(ci.Topics) > 0 {
		return ci.Topics
	}
	if ci.Topic != "" {
		return []string{ci.Topic}
	}
	return nil
}

// commandAction сопоставляет payload внешней интеграции с действием устройства.
func commandAction(ci CommandIntegrationConfig, raw string) (string, bool) {
	value := strings.ToLower(strings.Trim(strings.TrimSpace(raw), "\"'"))
	for _, candidate := range ci.PayloadOn {
		if value == strings.ToLower(strings.TrimSpace(candidate)) {
			return ci.ActionOn, true
		}
	}
	for _, candidate := range ci.PayloadOff {
		if value == strings.ToLower(strings.TrimSpace(candidate)) {
			return ci.ActionOff, true
		}
	}
	return "", false
}

// parseBoolPayload понимает true/false, on/off, 1/0 и ВКЛ/ВЫКЛ.
func parseBoolPayload(raw string) (bool, bool) {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(raw), "\"'")) {
	case "true", "on", "1", "вкл":
		return true, true
	case "false", "off", "0", "выкл":
		return false, true
	default:
		return false, false
	}
}

// isTruePayload возвращает true только для payload, распознанного как включение.
func isTruePayload(raw string) bool {
	v, ok := parseBoolPayload(raw)
	return ok && v
}

// DeviceIDByTopic ищет устройство по MQTT-topic, сравнивая ZigbeeID и YandexID.
func (a *App) DeviceIDByTopic(topic string) string {
	for _, d := range a.cfg.Devices {
		if d.ZigbeeID != "" && strings.Contains(topic, d.ZigbeeID) {
			return d.ID
		}

	}
	return ""
}

// TouchDevice обновляет только last_seen устройства при любом входящем сообщении от него.
func (a *App) TouchDevice(id string) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if _, ok := a.state.Devices[id]; !ok {
		a.state.Devices[id] = map[string]any{}
	}
	a.state.Devices[id]["last_seen"] = time.Now().Unix()
}

// isRepeatedTrueEvent предотвращает повторные аварийные действия, если состояние уже было true.
func isRepeatedTrueEvent(ev EventConfig, prevState map[string]any) bool {
	if len(ev.When) == 0 || len(ev.Actions) == 0 && ev.Notify == nil {
		return false
	}
	for field, want := range ev.When {
		wantBool, ok := toBool(want)
		if !ok || !wantBool {
			continue
		}
		if prevBool, ok := toBool(prevState[field]); ok && prevBool {
			return true
		}
	}
	return false
}

// LogImportantStateChange пишет в лог важные переходы состояний, например появление/уход протечки.
func (a *App) LogImportantStateChange(ev EventConfig, prevState map[string]any) {
	if ev.DeviceID == "" {
		return
	}
	current := a.getDeviceState(ev.DeviceID)
	if oldLeak, oldOK := toBool(prevState["water_leak"]); oldOK {
		if newLeak, newOK := toBool(current["water_leak"]); newOK && oldLeak != newLeak {
			if d, ok := a.devices[ev.DeviceID]; ok {
				if newLeak {
					logWarn("leak detected: device=%s name=%q", ev.DeviceID, d.Name)
				} else {
					logInfo("leak cleared: device=%s name=%q", ev.DeviceID, d.Name)
				}
			}
		}
	}
}

// ApplyUpdates применяет правила обновления состояния устройства на основе MQTT-payload.
func (a *App) ApplyUpdates(deviceID string, updates map[string]string, payload map[string]any, raw string) bool {
	if len(updates) == 0 {
		return false
	}
	changed := false
	for field, expr := range updates {
		value := evalExpression(expr, payload, raw)
		if value == nil {
			logDebug("state update skipped: device=%s field=%s expression=%q reason=nil", deviceID, field, expr)
			continue
		}
		a.SetDeviceValue(deviceID, field, value)
		changed = true
	}
	return changed
}

// whenMatches проверяет, соответствует ли MQTT-payload условиям события.
func whenMatches(when map[string]any, payload map[string]any, raw string) bool {
	if len(when) == 0 {
		return true
	}
	for k, want := range when {
		var got any
		if k == "$raw" {
			got = strings.TrimSpace(raw)
		} else {
			got = payload[k]
		}
		if !valuesEqual(got, want) {
			return false
		}
	}
	return true
}

// evalExpression вычисляет простое выражение из конфигурации для обновления состояния или отчёта.
func evalExpression(expr string, payload map[string]any, raw string) any {
	if expr == "$raw_bool" {
		v, ok := parseBoolPayload(raw)
		if !ok {
			return nil
		}
		return v
	}
	if expr == "$raw" {
		return raw
	}
	if strings.HasPrefix(expr, "payload.") {
		field := strings.TrimPrefix(expr, "payload.")
		if value, ok := payload[field]; ok {
			return value
		}
		return nil
	}
	if strings.HasPrefix(expr, "eq(payload.") {
		inside := strings.TrimSuffix(strings.TrimPrefix(expr, "eq(payload."), ")")
		parts := strings.SplitN(inside, ",", 2)
		if len(parts) == 2 {
			field := strings.TrimSpace(parts[0])
			value, ok := payload[field]
			if !ok {
				return nil
			}
			want := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
			return fmt.Sprint(value) == want
		}
	}
	if strings.HasPrefix(expr, "ne(payload.") {
		inside := strings.TrimSuffix(strings.TrimPrefix(expr, "ne(payload."), ")")
		parts := strings.SplitN(inside, ",", 2)
		if len(parts) == 2 {
			field := strings.TrimSpace(parts[0])
			value, ok := payload[field]
			if !ok {
				return nil
			}
			want := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
			return fmt.Sprint(value) != want
		}
	}
	if strings.HasPrefix(expr, "and(") && strings.HasSuffix(expr, ")") {
		inside := strings.TrimSuffix(strings.TrimPrefix(expr, "and("), ")")
		for _, part := range splitArgs(inside) {
			result := evalExpression(strings.TrimSpace(part), payload, raw)
			if result == nil {
				return nil
			}
			v, ok := result.(bool)
			if !ok || !v {
				return false
			}
		}
		return true
	}
	if strings.HasPrefix(expr, "pressure_mmhg(payload.") {
		field := strings.TrimSuffix(strings.TrimPrefix(expr, "pressure_mmhg(payload."), ")")
		value, ok := payload[field]
		if !ok {
			return nil
		}
		return math.Round(toFloat(value) / 1.333)
	}
	return expr
}

// splitArgs делит аргументы простых выражений конфигурации, не разрывая вложенные скобки.
func splitArgs(s string) []string {
	args := []string{}
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				args = append(args, s[start:i])
				start = i + 1
			}
		}
	}
	args = append(args, s[start:])
	return args
}

// evalReport вычисляет значение отчёта устройства для публикации в MQTT.
func evalReport(r ReportConfig, payload map[string]any, raw string) any {
	if r.Expression != "" {
		return evalExpression(r.Expression, payload, raw)
	}
	return payload[r.Field]
}

// toFloat безопасно приводит числовое значение из JSON или Go-типа к float64.
func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int8:
		return float64(x)
	case int16:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case uint:
		return float64(x)
	case uint8:
		return float64(x)
	case uint16:
		return float64(x)
	case uint32:
		return float64(x)
	case uint64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	default:
		return 0
	}
}

// topicMatch проверяет совпадение MQTT-topic с точным шаблоном или wildcard /#.
func topicMatch(pattern, topic string) bool {
	return pattern == topic || pattern == "#" || strings.HasSuffix(pattern, "/#") && strings.HasPrefix(topic, strings.TrimSuffix(pattern, "#"))
}
