package main

import (
	"encoding/json"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"sync"
	"time"
)

const (
	defaultConfigPath = "/etc/smarthome.conf"
	defaultLastSeen   = int64(1735671600)
)

type logLevel int

const (
	levelDebug logLevel = iota
	levelInfo
	levelWarn
	levelError
)

var currentLogLevel = levelInfo

type Config struct {
	Telegram            TelegramConfig             `json:"telegram"`
	MQTT                MQTTConfig                 `json:"mqtt"`
	Storage             StorageConfig              `json:"storage"`
	Logging             LoggingConfig              `json:"logging"`
	Labels              LabelsConfig               `json:"labels"`
	VideoMonitoring     VideoMonitoringConfig      `json:"video_monitoring"`
	Devices             []DeviceConfig             `json:"devices"`
	CommandIntegrations []CommandIntegrationConfig `json:"command_integrations"`
	Groups              []GroupConfig              `json:"groups"`
	Menu                MenuConfig                 `json:"menu"`
	Events              []EventConfig              `json:"events"`
	Schedules           []ScheduleConfig           `json:"schedules"`
}

type TelegramConfig struct {
	Token          string       `json:"token"`
	WelcomeMessage string       `json:"welcome_message"`
	AllowedUsers   []UserConfig `json:"allowed_users"`
	Proxy          ProxyConfig  `json:"proxy"`
	PollTimeoutSec int          `json:"poll_timeout_sec"`
	HTTPTimeoutSec int          `json:"http_timeout_sec"`
}

type UserConfig struct {
	ID                    int64 `json:"id"`
	VideoMonitoringEnable bool  `json:"video_monitoring_enabled"`
}

type ProxyConfig struct {
	Enabled  bool   `json:"enabled"`
	Type     string `json:"type"` // none, socks5, mtproxy
	Address  string `json:"address"`
	Username string `json:"username"`
	Password string `json:"password"`
	Secret   string `json:"secret"` // зарезервировано для MTProxy
}

type MQTTConfig struct {
	Server      string   `json:"server"`
	Login       string   `json:"login"`
	Password    string   `json:"password"`
	ClientID    string   `json:"client_id"`
	QOS         byte     `json:"qos"`
	Retained    bool     `json:"retained"`
	BaseTopics  []string `json:"base_topics"`
	SubscribeTo []string `json:"subscribe_to"`
}

type StorageConfig struct {
	StateFile string `json:"state_file"`
}

// LoggingConfig описывает, куда и с каким уровнем подробности писать журналы приложения.
type LoggingConfig struct {
	Level     string `json:"level"`      // debug, info, warn, error
	File      string `json:"file"`       // пусто = писать только в stdout/stderr
	ToConsole bool   `json:"to_console"` // true = дополнительно писать в консоль
}

type LabelsConfig struct {
	Off       string `json:"off"`
	On        string `json:"on"`
	Yes       string `json:"yes"`
	No        string `json:"no"`
	Unknown   string `json:"unknown_command"`
	DateTime  string `json:"datetime_format"`
	ShortTime string `json:"short_datetime_format"`
}

type VideoMonitoringConfig struct {
	Enabled        bool     `json:"enabled"`
	LockFile       string   `json:"lock_file"`
	CheckInterval  Duration `json:"check_interval"`
	DeleteLockFile bool     `json:"delete_lock_file"`
}

type DeviceConfig struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Type        string                  `json:"type"` // switch, sensor, alarm, virtual
	Description string                  `json:"description"`
	ZigbeeID    string                  `json:"zigbee_id"`
	YandexID    string                  `json:"yandex_id"`
	Subscribe   []string                `json:"subscribe"`
	Initial     map[string]any          `json:"initial"`
	Actions     map[string]ActionConfig `json:"actions"`
	Reports     []ReportConfig          `json:"reports"`
	Fields      map[string]string       `json:"fields"`
}

type ActionConfig struct {
	Title        string          `json:"title"`
	SetState     *bool           `json:"set_state,omitempty"`
	Force        bool            `json:"force,omitempty"`
	Publishes    []PublishConfig `json:"publishes"`
	RunActions   []string        `json:"run_actions"`
	SetUserVideo *bool           `json:"set_user_video,omitempty"`
}

type PublishConfig struct {
	Topic   string `json:"topic"`
	Payload any    `json:"payload"`
}

type CommandIntegrationConfig struct {
	ID         string   `json:"id"`
	Enabled    bool     `json:"enabled"`
	Topic      string   `json:"topic"`
	Topics     []string `json:"topics"`
	ActionOn   string   `json:"action_on"`
	ActionOff  string   `json:"action_off"`
	PayloadOn  []string `json:"payload_on"`
	PayloadOff []string `json:"payload_off"`
	Force      bool     `json:"force"`
}

type ReportConfig struct {
	Name       string `json:"name"`
	Topic      string `json:"topic"`
	Field      string `json:"field"`
	Expression string `json:"expression"` // bool, float, int, string; pressure_mmhg поддержан отдельно
}

type GroupConfig struct {
	ID      string                  `json:"id"`
	Name    string                  `json:"name"`
	Members []string                `json:"members"`
	Actions map[string]ActionConfig `json:"actions"`
}

type MenuConfig struct {
	StartCommand string       `json:"start_command"`
	ButtonWidth  int          `json:"button_width"`
	Rows         [][]MenuItem `json:"rows"`
}

type MenuItem struct {
	Text     string `json:"text"`
	DeviceID string `json:"device_id"`
	GroupID  string `json:"group_id"`
	Action   string `json:"action"`
	Builtin  string `json:"builtin"` // status, alarms, batteries
}

type EventConfig struct {
	ID        string            `json:"id"`
	OnTopic   string            `json:"on_topic"`
	DeviceID  string            `json:"device_id"`
	When      map[string]any    `json:"when"`
	Updates   map[string]string `json:"updates"`
	Publishes []PublishConfig   `json:"publishes"`
	Actions   []string          `json:"actions"`
	Notify    *NotifyConfig     `json:"notify,omitempty"`
}

type NotifyConfig struct {
	Users string `json:"users"` // all, video_enabled
	Text  string `json:"text"`
}

type ScheduleConfig struct {
	ID     string `json:"id"`
	At     string `json:"at"` // HH:MM
	Action string `json:"action"`
}

type Duration struct{ time.Duration }

// UnmarshalJSON разбирает длительность из JSON: строку вроде "1s" или число секунд.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		d.Duration = v
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	d.Duration = time.Duration(n) * time.Second
	return nil
}

type State struct {
	Devices map[string]map[string]any `json:"devices"`
	Users   map[string]UserState      `json:"users"`
}

type UserState struct {
	VideoMonitoringEnabled bool `json:"video_monitoring_enabled"`
}

type App struct {
	cfg       Config
	state     State
	stateMu   sync.RWMutex
	devices   map[string]DeviceConfig
	groups    map[string]GroupConfig
	users     map[int64]UserConfig
	menuIndex map[string]MenuItem
	bot       *tgbotapi.BotAPI
	mqtt      mqtt.Client
}
