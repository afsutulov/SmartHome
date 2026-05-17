package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

const (
	appName        = "SmartHome"
	appVersion     = "1.0.0"
	appDescription = "Lightweight MQTT smart home controller for Telegram, zigbee2mqtt and yandex2mqtt."
)

// main показывает информацию о приложении либо запускает сервисный режим при --run.
func main() {
	var configPath string
	var run bool
	var showVersion bool

	flag.StringVar(&configPath, "config", defaultConfigPath, "path to JSON config file")
	flag.StringVar(&configPath, "c", defaultConfigPath, "path to JSON config file")
	flag.BoolVar(&run, "run", false, "run SmartHome service")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&showVersion, "v", false, "print version and exit")
	flag.Usage = printUsage
	flag.Parse()

	if showVersion {
		fmt.Printf("%s %s\n", appName, appVersion)
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
	app := NewApp(cfg)
	if err := app.LoadState(); err != nil {
		logWarn("state load warning: %v", err)
	}

	bot, err := app.ConnectTelegram()
	if err != nil {
		return fmt.Errorf("telegram error: %w", err)
	}
	app.bot = bot

	app.mqtt = app.ConnectMQTT()
	app.SubscribeMQTT()
	go app.ProcessTelegram()
	go app.RunSchedules()
	go app.RunVideoMonitoring()

	select {}
}
