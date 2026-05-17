# SmartHome

SmartHome is a lightweight MQTT-based smart home controller written in Go.
It is designed as a small, transparent replacement for Home Assistant when you only need a configurable core for MQTT devices, Telegram control, zigbee2mqtt, yandex2mqtt, schedules, simple automation and notifications.

SmartHome keeps application logic in Go and keeps devices, Telegram menu, MQTT topics, automation rules and integrations in a JSON configuration file.

## Features

- MQTT-first architecture.
- Works with `zigbee2mqtt` devices.
- Works with `yandex2mqtt` for Yandex Alice / Yandex Smart Home integrations.
- Telegram bot keyboard configured from JSON.
- Devices, groups, actions, events and schedules configured without recompiling.
- Runtime state file separate from configuration.
- Optional video monitoring trigger file.
- Optional Telegram SOCKS5 proxy.
- Systemd-friendly service mode.

## Project status

This project is intended for advanced home users who prefer a compact, auditable service instead of a large automation platform.
It does not try to replace every Home Assistant feature. It focuses on:

- MQTT device state handling;
- simple on/off actions;
- sensors and leak alarms;
- Telegram control;
- Yandex Alice via yandex2mqtt;
- basic automation and schedules.

## Runtime modes

By default SmartHome does not start the service. It prints the product name, version and available command-line options.

```bash
./SmartHome
```

Run the service explicitly:

```bash
./SmartHome --run --config /etc/smarthome.conf
```

Available options:

```text
--run              run SmartHome as a service
--config, -c PATH  path to JSON config file, default /etc/smarthome.conf
--version, -v      print version and exit
--help, -h         show help
```

## Requirements

- Linux host or server.
- Go 1.23 or newer for building from source.
- MQTT broker, for example Mosquitto.
- Optional: zigbee2mqtt.
- Optional: yandex2mqtt.
- Optional: Telegram bot token from BotFather.

## Build

```bash
git clone https://github.com/YOUR_USERNAME/SmartHome.git
cd SmartHome
go mod tidy
go build -o SmartHome .
```

## Install

Create directories:

```bash
sudo mkdir -p /etc /var/lib/smarthome /var/log/smarthome
sudo install -m 0755 SmartHome /usr/local/bin/SmartHome
```

Install a configuration file:

```bash
sudo cp configs/smarthome.conf.example.json /etc/smarthome.conf
sudo nano /etc/smarthome.conf
```

Validate JSON syntax:

```bash
jq . /etc/smarthome.conf
```

Run manually:

```bash
/usr/local/bin/SmartHome --run --config /etc/smarthome.conf
```

## Run as a systemd service

Install the unit:

```bash
sudo cp systemd/smarthome.service /etc/systemd/system/smarthome.service
sudo systemctl daemon-reload
sudo systemctl enable smarthome
sudo systemctl start smarthome
```

Check logs:

```bash
journalctl -u smarthome -f
```

If you log to a file in the config, also check:

```bash
tail -f /var/log/smarthome/smarthome.log
```

## Configuration

Default configuration path:

```text
/etc/smarthome.conf
```

Config format is JSON. See:

- `configs/smarthome.conf.example.json` — valid JSON example you can copy and run.
- `configs/smarthome.conf.example.jsonc` — documented example with comments.
- `docs/CONFIGURATION.md` — configuration guide.

Important idea: the Go code should stay a generic core. Your concrete devices, MQTT topics, actions, Telegram menu and events belong in the config file.

## MQTT topic model

Typical setup:

```text
zigbee2mqtt/<device_id>          incoming device state
zigbee2mqtt/<device_id>/set      outgoing command to device
/yandex/<yandex_id>              command from yandex2mqtt
/yandex/<yandex_id>/state        state report to yandex2mqtt
```

The exact topic names are configurable.

## yandex2mqtt integration

SmartHome uses `command_integrations` to handle external MQTT command systems.
For yandex2mqtt, commands usually arrive on:

```text
/yandex/<device_id>
```

State should be published to:

```text
/yandex/<device_id>/state
```

Example:

```json
{
  "id": "yandex",
  "enabled": true,
  "topics": [
    "/{{index .BaseTopic 1}}/{{.Device.YandexID}}",
    "{{index .BaseTopic 1}}/{{.Device.YandexID}}"
  ],
  "action_on": "on",
  "action_off": "off",
  "payload_on": ["true", "on", "1"],
  "payload_off": ["false", "off", "0"],
  "force": true
}
```

Use `mosquitto_sub` to debug:

```bash
mosquitto_sub -h 127.0.0.1 -t '/yandex/#' -v
```

## Telegram bot

Create a bot with BotFather and put the token into `telegram.token`.
Add your Telegram numeric user ID to `telegram.allowed_users`.

Start the bot:

```text
/start
```

The keyboard is generated from the `menu.rows` section of the config.

## Development

Format code:

```bash
gofmt -w *.go
```

Build:

```bash
go build -o SmartHome .
```

## Optional install script

After building the binary, you can install SmartHome as a systemd service with:

```bash
sudo scripts/install-systemd.sh
```

The script creates a `smarthome` system user, installs the binary to `/usr/local/bin/SmartHome`, installs `systemd/smarthome.service`, and creates `/var/lib/smarthome` and `/var/log/smarthome`.

## License

Choose a license before publishing. MIT is a common choice for small infrastructure projects.
