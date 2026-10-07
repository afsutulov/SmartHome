package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"text/template"
)

// baseTopic возвращает базовый MQTT-topic по индексу из конфигурации.
func (a *App) baseTopic(i int) string {
	if i < len(a.cfg.MQTT.BaseTopics) {
		return a.cfg.MQTT.BaseTopics[i]
	}
	return ""
}

// RenderTopic подставляет параметры устройства в шаблон MQTT-topic.
func (a *App) RenderTopic(t, deviceID string) string {
	if d, ok := a.devices[deviceID]; ok {
		return a.RenderWithDevice(t, d, nil)
	}
	return render(t, nil)
}

// RenderWithDevice рендерит строковый шаблон с данными устройства, базовых топиков и payload.
func (a *App) RenderWithDevice(t string, d DeviceConfig, payload map[string]any) string {
	return render(t, map[string]any{"Device": d, "BaseTopic": a.cfg.MQTT.BaseTopics, "Payload": payload})
}

// RenderPayload рендерит MQTT-payload из строки или JSON-объекта конфигурации.
func (a *App) RenderPayload(p any, d DeviceConfig, payload map[string]any) string {
	return renderPayload(p, map[string]any{"Device": d, "BaseTopic": a.cfg.MQTT.BaseTopics, "Payload": payload})
}

// renderPayload преобразует payload из конфигурации в строку с подстановкой шаблонов.
func renderPayload(p any, data any) string {
	result, err := renderPayloadChecked(p, data)
	if err != nil {
		logError("payload template error: %v", err)
		return ""
	}
	return result
}

func renderPayloadChecked(p any, data any) (string, error) {
	switch v := p.(type) {
	case string:
		return renderChecked(v, data)
	default:
		return renderChecked(jsonString(v), data)
	}
}

// Ошибка шаблона не означает успешную отправку исходного текста в MQTT.
func (a *App) publishConfigured(p PublishConfig, data any) bool {
	topic, err := renderChecked(p.Topic, data)
	if err != nil {
		logError("publish topic template error: %v", err)
		return false
	}
	payload, err := renderPayloadChecked(p.Payload, data)
	if err != nil {
		logError("publish payload template error: topic=%s error=%v", topic, err)
		return false
	}
	return a.Publish(topic, payload)
}

// render выполняет text/template-шаблон и возвращает исходную строку при ошибке.
func render(t string, data any) string {
	result, err := renderChecked(t, data)
	if err != nil {
		logError("template error: %v", err)
		return t
	}
	return result
}

func renderChecked(t string, data any) (string, error) {
	tmpl, err := template.New("x").Option("missingkey=error").Funcs(template.FuncMap{"json": mustJSON}).Parse(t)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// mustJSON возвращает JSON-представление значения для использования внутри шаблонов.
func mustJSON(v any) string { return jsonString(v) }

// jsonString сериализует значение в компактный JSON без HTML-экранирования.
func jsonString(v any) string {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return "null"
	}
	return strings.TrimSpace(b.String())
}
