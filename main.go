package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

const (
	appName        = "SmartHome"
	appVersion     = "1.0.7"
	appDescription = "Lightweight MQTT smart home controller for Telegram, zigbee2mqtt and yandex2mqtt."
)

// main показывает информацию о приложении либо запускает сервисный режим при --run.
func main() {
	var configPath string
	var run bool
	var showVersion bool
	var checkConfig bool

	flag.StringVar(&configPath, "config", defaultConfigPath, "path to JSON config file")
	flag.StringVar(&configPath, "c", defaultConfigPath, "path to JSON config file")
	flag.BoolVar(&checkConfig, "check-config", false, "validate configuration without connecting to services")
	flag.BoolVar(&run, "run", false, "run SmartHome service")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&showVersion, "v", false, "print version and exit")
	flag.Usage = printUsage
	flag.Parse()

	if showVersion {
		fmt.Printf("%s %s\n", appName, appVersion)
		return
	}

	if checkConfig {
		cfg, err := loadConfig(configPath)
		if err == nil {
			err = ValidateConfig(cfg)
		}
		if err != nil {
			log.Fatalf("config validation error: %v", err)
		}
		fmt.Println("Configuration OK")
		return
	}

	if !run {
		printUsage()
		return
	}

	if err := runService(configPath); err != nil {
		log.Fatalf("service error: %v", err)
	}
}

// printUsage выводит краткое приветствие, описание версии и доступные ключи запуска.
func printUsage() {
	fmt.Fprintf(os.Stdout, "%s %s\n", appName, appVersion)
	fmt.Fprintf(os.Stdout, "%s\n\n", appDescription)
	fmt.Fprintln(os.Stdout, "Usage:")
	fmt.Fprintf(os.Stdout, "  %s --run [--config %s]\n\n", os.Args[0], defaultConfigPath)
	fmt.Fprintln(os.Stdout, "Options:")
	fmt.Fprintln(os.Stdout, "  --check-config     validate config without MQTT or Telegram")
	fmt.Fprintln(os.Stdout, "  --run              run SmartHome as a service")
	fmt.Fprintf(os.Stdout, "  --config, -c PATH  path to JSON config file (default: %s)\n", defaultConfigPath)
	fmt.Fprintln(os.Stdout, "  --version, -v      print version and exit")
	fmt.Fprintln(os.Stdout, "  --help, -h         show this help")
}

// runService загружает конфигурацию, инициализирует приложение и запускает рабочие процессы.
func runService(configPath string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}
	if err := initLogging(cfg.Logging); err != nil {
		return fmt.Errorf("logging error: %w", err)
	}
	if err := ValidateConfig(cfg); err != nil {
		return fmt.Errorf("config validation error: %w", err)
	}
	logInfo("config loaded: %s", configPath)
	logConfigWarnings(cfg)
	app := NewApp(cfg)
	if err := app.LoadState(); err != nil {
		if !errors.Is(err, errStateRecovered) {
			// Отказ от запуска означал бы полное отсутствие защиты от протечек.
			// Работаем со значениями по умолчанию, но state-файл не перезаписываем.
			app.statePersistenceDisabled = true
			logError("state load failed, running WITHOUT state persistence (original file is kept untouched): %v", err)
			app.notifyAllUsers("SmartHome запущен без сохранения состояния: state-файл не читается. Закрытие воды и сирена доступны; открытие воды заблокировано до получения состояния защитных датчиков. Проверьте журнал и файл " + cfg.Storage.StateFile)
		} else {
			logWarn("state load warning: %v", err)
			app.notifyAllUsers("SmartHome: повреждённое состояние сохранено в резервную копию. Открытие воды заблокировано до получения состояния защитных датчиков. Проверьте журнал и файл " + cfg.Storage.StateFile)
		}
	}

	// Защита от протечек работает даже при недоступном Telegram.
	app.ConnectMQTT()
	go app.handleShutdown()
	go app.RunSchedules()
	bot, err := app.ConnectTelegram()
	if err != nil {
		return fmt.Errorf("telegram error: %w", err)
	}
	app.bot = bot

	go app.RunNotifications()
	go app.ProcessTelegram()
	go app.RunVideoMonitoring()

	select {}
}

// handleShutdown корректно завершает сервис по SIGTERM/SIGINT (systemctl stop/restart):
// сохраняет состояние и закрывает MQTT-сессию вместо обрыва соединения.
func (a *App) handleShutdown() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	s := <-sig
	logInfo("shutdown: signal=%v", s)
	a.SaveState()
	if a.mqtt != nil {
		a.mqtt.Disconnect(250)
	}
	os.Exit(0)
}

// logConfigWarnings пишет предупреждения о допустимых, но рискованных настройках.
func logConfigWarnings(cfg Config) {
	if cfg.MQTT.Retained {
		logWarn("mqtt.retained=true: commands to */set topics will be retained by the broker and replayed to devices after their restart")
	}
	if len(cfg.Telegram.AllowedUsers) == 0 {
		logWarn("telegram.allowed_users is empty: the bot will ignore all messages")
	}
	if cfg.VideoMonitoring.Enabled && !cfg.VideoMonitoring.DeleteLockFile {
		logInfo("video_monitoring.delete_lock_file=false: each new version of %s is processed once", cfg.VideoMonitoring.LockFile)
	}
}
