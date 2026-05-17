package main

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// RunDeviceAction находит устройство и действие по ID и запускает это действие.
func (a *App) RunDeviceAction(deviceID, actionName string, chatID int64) string {
	return a.runDeviceActionInternal(deviceID, actionName, chatID, "", false)
}

// RunDeviceActionFromIntegration запускает действие устройства по команде внешней интеграции
// (например, Яндекс Алисы). В этом случае пропускаем публикации в топики данной интеграции,
// чтобы не создавать петлю: интеграция -> action -> publish -> интеграция -> ...
func (a *App) RunDeviceActionFromIntegration(deviceID, actionName, integrationID string, force bool) string {
	return a.runDeviceActionInternal(deviceID, actionName, 0, integrationID, force)
}

// runDeviceActionInternal — внутренняя реализация запуска действия устройства.
func (a *App) runDeviceActionInternal(deviceID, actionName string, chatID int64, skipIntegrationID string, force bool) string {
	d, ok := a.devices[deviceID]
	if !ok {
		logWarn("device action failed: unknown device=%s action=%s", deviceID, actionName)
		return "Устройство не найдено"
	}
	act, ok := d.Actions[actionName]
	if !ok {
		logWarn("device action failed: device=%s unknown action=%s", deviceID, actionName)
		return "Действие не найдено"
	}
	return a.runAction(d, act, chatID, skipIntegrationID, force)
}

// RunGroupAction выполняет действие группы или применяет одноимённое действие ко всем участникам группы.
func (a *App) RunGroupAction(groupID, actionName string, chatID int64) string {
	g, ok := a.groups[groupID]
	if !ok {
		return "Группа не найдена"
	}
	act, ok := g.Actions[actionName]
	if ok {
		return a.runGroupAction(g, act, chatID)
	}
	for _, id := range g.Members {
		a.RunDeviceAction(id, actionName, chatID)
	}
	return fmt.Sprintf("%s: <b>%s</b>", html.EscapeString(g.Name), html.EscapeString(actionName))
}

// runAction выполняет конкретное действие устройства: публикации MQTT, изменение состояния и цепочки действий.
// skipIntegrationID: если непустой, пропускаем публикации в топики данной command integration
// (защита от петли при командах от внешних интеграций типа Яндекс Алисы).
//
// ПОРЯДОК ОПЕРАЦИЙ: сначала обновляем state, потом публикуем в MQTT.
// Это критично для защиты от петли: когда мы публикуем в /yandex/DeviceID,
// брокер немедленно эхо-отправляет нам наше же сообщение (мы подписаны на этот топик).
// Если state уже обновлён ДО публикации — state guard (deviceStateEquals) заблокирует
// повторное выполнение действия. Если обновить state ПОСЛЕ — guard не сработает,
// потому что к моменту эхо state ещё хранит старое значение.
func (a *App) runAction(d DeviceConfig, act ActionConfig, chatID int64, skipIntegrationID string, force bool) string {
	if act.SetUserVideo != nil && chatID != 0 {
		if a.userVideoEnabled(chatID) == *act.SetUserVideo {
			return fmt.Sprintf("%s уже <b>%s</b>", html.EscapeString(d.Name), a.boolLabel(*act.SetUserVideo))
		}
		a.setUserVideo(chatID, *act.SetUserVideo)
		return fmt.Sprintf("%s: <b>%s</b>", html.EscapeString(d.Name), a.boolLabel(*act.SetUserVideo))
	}
	if !force && !act.Force && act.SetState != nil && a.deviceStateEquals(d.ID, "state", *act.SetState) {
		logDebug("skip action, state already set: device=%s state=%v skip_integration=%s force=%v", d.ID, *act.SetState, skipIntegrationID, force)
		return fmt.Sprintf("%s уже <b>%s</b>", html.EscapeString(d.Name), a.boolLabel(*act.SetState))
	}
	logInfo("device action: device=%s name=%q action_title=%q set_state=%v chat_id=%d skip_integration=%s force=%v", d.ID, d.Name, act.Title, act.SetState, chatID, skipIntegrationID, force)

	// Обновляем state ДО публикации в MQTT.
	// Когда сообщение в /yandex/DeviceID придёт обратно к нам эхом от брокера,
	// state уже будет равен новому значению → state guard заблокирует повторное выполнение.
	if act.SetState != nil {
		a.SetDeviceValue(d.ID, "state", *act.SetState)
	}

	for _, p := range act.Publishes {
		topic := a.RenderWithDevice(p.Topic, d, nil)
		// Защита от петли: при команде от интеграции не публикуем обратно в её топик.
		// Яндекс получит подтверждение через reports после ответа zigbee-устройства.
		if skipIntegrationID != "" && a.isIntegrationTopic(skipIntegrationID, topic, d) {
			logDebug("skip publish to integration topic (loop prevention): integration=%s topic=%s", skipIntegrationID, topic)
			continue
		}
		a.Publish(topic, a.RenderPayload(p.Payload, d, nil))
	}
	for _, next := range act.RunActions {
		a.RunNamedAction(next, chatID)
	}
	a.SaveState()
	if act.Title != "" {
		return fmt.Sprintf("%s: <b>%s</b>", html.EscapeString(d.Name), html.EscapeString(act.Title))
	}
	if act.SetState != nil {
		return fmt.Sprintf("%s: <b>%s</b>", html.EscapeString(d.Name), a.boolLabel(*act.SetState))
	}
	return fmt.Sprintf("%s: выполнено", html.EscapeString(d.Name))
}

// runGroupAction выполняет действие группы: вложенные действия, MQTT-публикации и сохранение состояния.
func (a *App) runGroupAction(g GroupConfig, act ActionConfig, chatID int64) string {
	for _, next := range act.RunActions {
		a.RunNamedAction(next, chatID)
	}
	for _, p := range act.Publishes {
		a.Publish(render(p.Topic, nil), renderPayload(p.Payload, nil))
	}
	a.SaveState()
	return fmt.Sprintf("%s: <b>%s</b>", html.EscapeString(g.Name), html.EscapeString(act.Title))
}

// RunNamedAction выполняет действие по ссылке вида "device.action" или "group.action".
func (a *App) RunNamedAction(ref string, chatID int64) {
	parts := strings.Split(ref, ".")
	if len(parts) != 2 {
		return
	}
	if _, ok := a.devices[parts[0]]; ok {
		a.RunDeviceAction(parts[0], parts[1], chatID)
		return
	}
	if _, ok := a.groups[parts[0]]; ok {
		a.RunGroupAction(parts[0], parts[1], chatID)
		return
	}
}

// isIntegrationTopic проверяет, принадлежит ли данный topic к топикам command integration.
// Используется для предотвращения петли при обработке команд от внешних интеграций.
func (a *App) isIntegrationTopic(integrationID, topic string, d DeviceConfig) bool {
	for _, ci := range a.cfg.CommandIntegrations {
		if ci.ID != integrationID || !ci.Enabled {
			continue
		}
		for _, topicTpl := range commandIntegrationTopics(ci) {
			rendered := a.RenderWithDevice(topicTpl, d, nil)
			if sameMQTTTopic(topic, rendered) {
				return true
			}
		}
	}
	return false
}

// SetDeviceValue обновляет поле состояния устройства и выставляет текущий last_seen.
func (a *App) SetDeviceValue(id, field string, value any) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if _, ok := a.state.Devices[id]; !ok {
		a.state.Devices[id] = map[string]any{}
	}
	old := a.state.Devices[id][field]
	a.state.Devices[id][field] = value
	a.state.Devices[id]["last_seen"] = time.Now().Unix()
	if !valuesEqual(old, value) {
		if isLoggableStateField(field) {
			logInfo("state changed: device=%s field=%s old=%v new=%v", id, field, old, value)
		} else {
			logDebug("state changed: device=%s field=%s old=%v new=%v", id, field, old, value)
		}
	}
}

// isLoggableStateField определяет, какие поля состояния писать в info-лог.
func isLoggableStateField(field string) bool {
	switch field {
	case "state", "water_leak":
		return true
	default:
		return false
	}
}

// getDeviceState возвращает копию текущего состояния устройства.
func (a *App) getDeviceState(id string) map[string]any {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return cloneMap(a.state.Devices[id])
}

// deviceStateEquals проверяет, совпадает ли текущее поле состояния с ожидаемым значением.
func (a *App) deviceStateEquals(id, field string, want any) bool {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	got, ok := a.state.Devices[id][field]
	if !ok {
		return false
	}
	return valuesEqual(got, want)
}

// valuesEqual сравнивает значения из JSON/Go с учётом bool, чисел и строк.
func valuesEqual(a any, b any) bool {
	if ab, ok := toBool(a); ok {
		if bb, ok := toBool(b); ok {
			return ab == bb
		}
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// toBool безопасно приводит bool/string к bool для сравнения состояний.
func toBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "on", "1", "вкл":
			return true, true
		case "false", "off", "0", "выкл":
			return false, true
		}
	}
	return false, false
}
