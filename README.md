# SmartHome

English · [Русский](README.ru.md)

SmartHome is a Go service for home automation through MQTT. It receives device reports, keeps local state, executes JSON-defined rules and actions, and provides a Telegram control panel. It works with ZigbeeMQTTlink or zigbee2mqtt and optionally with yandex2mqtt and an external video recorder.

This guide describes installation, configuration, operation and troubleshooting of the current implementation.

## Contents

- [Architecture](#architecture)
- [Requirements and files](#requirements-and-files)
- [Quick start](#quick-start)
- [Linux installation with systemd](#linux-installation-with-systemd)
- [Windows](#windows)
- [Command-line options](#command-line-options)
- [Configuration rules](#configuration-rules)
- [Complete automation example](#complete-automation-example)
- [Connection and service settings](#connection-and-service-settings)
- [Devices and MQTT messages](#devices-and-mqtt-messages)
- [Actions, groups and protective guards](#actions-groups-and-protective-guards)
- [Events and expressions](#events-and-expressions)
- [Templates and outgoing reports](#templates-and-outgoing-reports)
- [Telegram menu](#telegram-menu)
- [External command integrations](#external-command-integrations)
- [Daily schedules](#daily-schedules)
- [Video monitoring](#video-monitoring)
- [State, backup and updates](#state-backup-and-updates)
- [Adding or replacing devices](#adding-or-replacing-devices)
- [Logs and troubleshooting](#logs-and-troubleshooting)
- [Acceptance checks and operating limits](#acceptance-checks-and-operating-limits)
- [Development and project layout](#development-and-project-layout)

## Architecture

| Component | Responsibility |
| --- | --- |
| Zigbee devices and coordinator | Radio communication, pairing and physical switching. |
| ZigbeeMQTTlink or zigbee2mqtt | Converts Zigbee messages to MQTT and MQTT commands to radio commands. |
| MQTT broker, for example Mosquitto | Routes messages between services. |
| SmartHome | Applies rules, maintains state, sends commands and Telegram notifications. |
| Telegram bot | User control and notifications. |
| yandex2mqtt, optional | Voice assistant commands and state integration. |
| Video recorder, optional | Produces a finished video and a trigger file for delivery. |

SmartHome connects to MQTT and uses Telegram long polling. It has no HTTP server or built-in web interface and does not access a USB coordinator. Pairing, Zigbee network creation, radio settings and coordinator backups belong to the Zigbee bridge.

A leak rule can receive `{"water_leak":true}`, send a close command to a valve, send `{"alarm":true}` to a siren and queue a notification.

A successful MQTT publication means the MQTT operation completed. It does **not** prove that a valve physically closed or a siren sounded.

## Requirements and files

For operation you need:

- A reachable MQTT broker with appropriate subscription/publication permissions.
- A configured Zigbee bridge already publishing your devices' reports.
- A Telegram bot token and allowed Telegram user IDs.
- A writable state directory, and a writable log directory if file logging is enabled.
- Outbound Telegram access, directly or through SOCKS5.

A nonempty Telegram token is required even if you mainly use MQTT automation. An empty `allowed_users` array is accepted, but the bot ignores all users. MQTT processing and schedules start before the Telegram connection succeeds.

Supplied binary targets:

| Platform | Architecture | File |
| --- | --- | --- |
| Linux | x86-64 / AMD64 | `bin/linux-amd64/SmartHome` |
| Linux | ARM64 / AArch64 | `bin/linux-arm64/SmartHome` |
| Windows | x86-64 / AMD64 | `bin/windows-amd64/SmartHome.exe` |

ARMv7 is not included in the release script. Go is needed to build sources, not to run a supplied binary. The module declares Go 1.25 and requests the Go 1.26.8 toolchain; automatic toolchain selection may download it during the first build.

Recommended Linux paths:

| Path | Purpose / permissions |
| --- | --- |
| `/usr/local/bin/SmartHome` | Executable; `root:root`, `0755`. |
| `/etc/smarthome.conf` | JSON configuration with credentials; `root:smarthome`, `0640`. |
| `/var/lib/smarthome/state.json` | Runtime state; written by `smarthome`, newly saved files use `0600`. |
| `/var/log/smarthome/smarthome.log` | Optional log; directory writable by `smarthome`. |
| `/etc/systemd/system/smarthome.service` | Service unit; `root:root`, `0644`. |

The `.conf` extension is a filename convention. The file contents are JSON.

## Quick start

From the unpacked project root on Linux x86-64:

```bash
uname -m
chmod +x bin/linux-amd64/SmartHome
mkdir -p run
chmod 700 run
cp configs/smarthome.conf.example.json run/config.json
```

On ARM64 use `linux-arm64` instead. For an initial connection test, replace the copied configuration with this minimal object and enter your bot token and user ID:

```json
{
  "telegram": {
    "token": "REPLACE_WITH_BOT_TOKEN",
    "welcome_message": "SmartHome is ready.",
    "allowed_users": [
      {"id":111111111,"video_monitoring_enabled":false}
    ]
  },
  "mqtt": {
    "server":"tcp://127.0.0.1:1883",
    "client_id":"SmartHome",
    "qos":1,
    "retained":false,
    "base_topics":["zigbee2mqtt"]
  },
  "storage":{"state_file":"./run/state.json"},
  "logging":{"level":"info","file":"","to_console":true},
  "menu":{
    "start_command":"/start",
    "rows":[[{"text":"Status","builtin":"status"}]]
  },
  "devices":[],
  "events":[],
  "groups":[],
  "schedules":[],
  "command_integrations":[]
}
```

An empty device list gives a connection-only setup. Add devices and events before expecting automation.

```bash
chmod 600 run/config.json
./bin/linux-amd64/SmartHome --check-config --config ./run/config.json
./bin/linux-amd64/SmartHome --run --config ./run/config.json
```

The check should print `Configuration OK`. Open a private chat with the bot, send `/start` and press **Status**. Stop with Ctrl+C.

`--check-config` validates JSON, references, expressions and topic templates. It does not connect to MQTT/Telegram, check credentials or physical devices, or prove that state/log directories are writable.

Running without `--run` displays help and exits.

## Linux installation with systemd

### Prepare the executable

Copy the supplied binary matching your CPU to the project root:

```bash
install -m 0755 bin/linux-amd64/SmartHome ./SmartHome
```

For ARM64 use this instead:

```bash
install -m 0755 bin/linux-arm64/SmartHome ./SmartHome
```

Alternatively build from source:

```bash
go version
go mod download
go mod verify
go build -o SmartHome .
```

`sha256sum -c SHA256SUMS` checks supplied binaries on Linux.

### Install

From the project root:

```bash
sudo bash scripts/install-systemd.sh
```

The script requires an executable `./SmartHome`. It creates the `smarthome` system user, state/log directories and service unit, installs the executable, enables startup at boot, and copies an example config if none exists. It does **not** start the service.

Existing `/etc/smarthome.conf` contents are preserved, but owner/mode are set to `root:smarthome` / `0640`. Ownership of standard state/log directories is changed recursively; reserve these directories for SmartHome.

### Configure and check access

```bash
sudo nano /etc/smarthome.conf
sudo -u smarthome /usr/local/bin/SmartHome --check-config --config /etc/smarthome.conf
sudo -u smarthome test -r /etc/smarthome.conf
sudo -u smarthome test -w /var/lib/smarthome
sudo -u smarthome test -w /var/log/smarthome
```

If the state file already exists:

```bash
sudo -u smarthome test -r /var/lib/smarthome/state.json
```

Custom paths need appropriate access to parent directories. Atomic state saves and video trigger claiming create/rename files in those directories.

To install a prepared configuration with correct permissions:

```bash
sudo install -o root -g smarthome -m 0640 ./config.json /etc/smarthome.conf
```

Validate a new file before replacing the working configuration, then validate the installed file as `smarthome`.

### Start and manage

```bash
sudo systemctl start smarthome
sudo systemctl status smarthome --no-pager
sudo journalctl -u smarthome -n 100 --no-pager
sudo journalctl -u smarthome -f
```

The unit runs `/usr/local/bin/SmartHome --run --config /etc/smarthome.conf` as `smarthome:smarthome`, with `/var/lib/smarthome` as working directory. Failed processes restart after five seconds.

```bash
sudo systemctl stop smarthome
sudo systemctl restart smarthome
sudo systemctl enable smarthome
sudo systemctl disable smarthome
```

Enable/disable affects startup at boot, not whether the process is currently running. Configuration changes require a restart; there is no hot reload.

### Manual installation

If you do not use the installer, create the user first unless it exists:

```bash
sudo useradd --system --home-dir /var/lib/smarthome --shell /usr/sbin/nologin smarthome
sudo install -d -o smarthome -g smarthome -m 0750 /var/lib/smarthome /var/log/smarthome
sudo install -o root -g root -m 0755 ./SmartHome /usr/local/bin/SmartHome
sudo install -o root -g smarthome -m 0640 ./config.json /etc/smarthome.conf
sudo install -o root -g root -m 0644 systemd/smarthome.service /etc/systemd/system/smarthome.service
sudo systemctl daemon-reload
sudo -u smarthome /usr/local/bin/SmartHome --check-config --config /etc/smarthome.conf
sudo systemctl enable --now smarthome
```

Use a configuration whose credentials, topics and device commands you have already checked.

## Windows

Build with Go from the project directory:

```powershell
go build -o SmartHome.exe .
```

Or run the supplied executable:

```powershell
New-Item -ItemType Directory -Force C:\SmartHome | Out-Null
Copy-Item .\configs\smarthome.conf.example.json C:\SmartHome\config.json
notepad C:\SmartHome\config.json
.\bin\windows-amd64\SmartHome.exe --check-config --config C:\SmartHome\config.json
.\bin\windows-amd64\SmartHome.exe --run --config C:\SmartHome\config.json
```

Use Windows paths in the configuration. Fragment:

```json
{
  "storage":{"state_file":"C:/SmartHome/state.json"},
  "logging":{"level":"info","file":"C:/SmartHome/smarthome.log","to_console":true}
}
```

Forward slashes avoid JSON backslash escaping. The account must be able to read the config and write selected directories. There is no supplied Windows service installer.

## Command-line options

| Option | Meaning |
| --- | --- |
| `--run` | Start the service. |
| `--config PATH` / `-c PATH` | JSON config path; default `/etc/smarthome.conf`. |
| `--check-config` | Validate configuration and exit without contacting services. |
| `--version` / `-v` | Print application version and exit. |
| `--help` / `-h` | Show help. |

Put options before positional arguments:

```bash
./SmartHome --version
./SmartHome --help
./SmartHome --check-config -c ./config.json
./SmartHome --run -c ./config.json
```

SIGTERM and SIGINT/Ctrl+C save state and disconnect MQTT. Shutdown is not a durable flush of every pending action or notification.

## Configuration rules

Use exactly one UTF-8 JSON object. Comments, trailing commas and unknown typed configuration fields are rejected. The `.jsonc` example is explanatory; remove comments before using it.

Boolean fields take `true`/`false`, not quoted strings. This matters especially for a siren's `alarm` command.

| Section | Purpose |
| --- | --- |
| `telegram` | Bot, allowed users, polling and proxy. |
| `mqtt` | Broker, QoS, retained flag, topics and subscriptions. |
| `storage` | Runtime state path. |
| `logging` | Severity and destinations. |
| `labels` | Display labels and date format. |
| `video_monitoring` | Video trigger watcher. |
| `devices` | Logical devices, state, actions and reports. |
| `groups` | Groups and grouped actions. |
| `events` | Incoming MQTT rules. |
| `command_integrations` | External on/off command sources. |
| `menu` | Telegram keyboard. |
| `schedules` | Daily actions. |

Choose stable, unique device/group IDs without dots, for example `bathroom_valve`. References are `object_id.action_name`. Device and group IDs must not overlap. Event IDs and schedule IDs must each be unique in their section.

Configuration is loaded at startup. Environment-variable interpolation and file inclusion are not implemented. Secrets stay in the protected config. Use absolute paths where practical: relative file paths resolve from the process working directory, not from the config's directory.

## Complete automation example

This example includes a leak sensor, valve, Tuya-style siren, weather sensor, button, light and away/home mode. It demonstrates guards, Telegram controls, reports, a leak notification, double-click siren silence and a daily light-off schedule.

Replace credentials, token, user ID and all `zigbee_id` values. Verify valve ON/OFF physically: the example assumes ON opens and OFF closes. Your wiring or protocol may differ. The bridge must already support these devices.

```json
{
  "telegram": {
    "token": "REPLACE_WITH_BOT_TOKEN",
    "welcome_message": "SmartHome control",
    "allowed_users": [
      {
        "id": 111111111,
        "video_monitoring_enabled": false
      }
    ],
    "poll_timeout_sec": 60,
    "http_timeout_sec": 70
  },
  "mqtt": {
    "server": "tcp://127.0.0.1:1883",
    "login": "REPLACE_WITH_MQTT_LOGIN",
    "password": "REPLACE_WITH_MQTT_PASSWORD",
    "client_id": "SmartHome",
    "qos": 1,
    "retained": false,
    "base_topics": [
      "zigbee2mqtt"
    ]
  },
  "storage": {
    "state_file": "/var/lib/smarthome/state.json"
  },
  "logging": {
    "level": "info",
    "file": "",
    "to_console": true
  },
  "labels": {
    "on": "ON",
    "off": "OFF",
    "yes": "YES",
    "no": "NO",
    "unknown_command": "Unknown command",
    "datetime_format": "2006-01-02 15:04"
  },
  "video_monitoring": {
    "enabled": false
  },
  "devices": [
    {
      "id": "leak_toilet",
      "name": "Toilet leak sensor",
      "type": "alarm",
      "zigbee_id": "0x00158d0000000001",
      "initial": {
        "battery": 0
      }
    },
    {
      "id": "water_valve",
      "name": "Water valve",
      "type": "switch",
      "zigbee_id": "0x00124b0000000002",
      "initial": {
        "state": false
      },
      "actions": {
        "on": {
          "set_state": true,
          "publishes": [
            {
              "topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}/set",
              "payload": {
                "state": "ON"
              }
            }
          ],
          "force": true,
          "block_when": [
            {
              "device_id": "leak_toilet",
              "field": "water_leak",
              "value": true
            }
          ]
        },
        "off": {
          "set_state": false,
          "publishes": [
            {
              "topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}/set",
              "payload": {
                "state": "OFF"
              }
            }
          ],
          "force": true
        }
      }
    },
    {
      "id": "siren",
      "name": "Siren",
      "type": "switch",
      "zigbee_id": "0xa4c1380000000003",
      "initial": {
        "state": false
      },
      "actions": {
        "on": {
          "set_state": true,
          "force": true,
          "publishes": [
            {
              "topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}/set",
              "payload": {
                "alarm": true
              }
            }
          ]
        },
        "off": {
          "set_state": false,
          "force": true,
          "publishes": [
            {
              "topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}/set",
              "payload": {
                "alarm": false
              }
            }
          ]
        }
      }
    },
    {
      "id": "weather",
      "name": "Room weather",
      "type": "sensor",
      "zigbee_id": "0x00158d0000000004",
      "initial": {
        "temperature": 0,
        "humidity": 0,
        "pressure": 0,
        "battery": 0
      },
      "reports": [
        {
          "name": "temperature",
          "topic": "smarthome/weather/temperature",
          "expression": "payload.temperature"
        }
      ]
    },
    {
      "id": "wall_button",
      "name": "Wall button",
      "type": "sensor",
      "zigbee_id": "0x00158d0000000005",
      "initial": {
        "battery": 0
      }
    },
    {
      "id": "living_light",
      "name": "Living room light",
      "type": "switch",
      "zigbee_id": "0x00124b0000000006",
      "initial": {
        "state": false
      },
      "actions": {
        "on": {
          "set_state": true,
          "publishes": [
            {
              "topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}/set",
              "payload": {
                "state": "ON"
              }
            }
          ]
        },
        "off": {
          "set_state": false,
          "publishes": [
            {
              "topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}/set",
              "payload": {
                "state": "OFF"
              }
            }
          ]
        }
      }
    },
    {
      "id": "home_mode",
      "name": "Away mode",
      "type": "virtual",
      "initial": {
        "state": false
      },
      "actions": {
        "on": {
          "title": "Away",
          "set_state": true,
          "run_actions": [
            "water_valve.off",
            "living_light.off"
          ]
        },
        "off": {
          "title": "Home",
          "set_state": false,
          "run_actions": [
            "water_valve.on",
            "living_light.on"
          ]
        }
      }
    }
  ],
  "groups": [
    {
      "id": "water_alarm",
      "name": "Water alarm",
      "members": [],
      "actions": {
        "trigger": {
          "title": "Close water and sound siren",
          "run_actions": [
            "water_valve.off",
            "siren.on"
          ]
        }
      }
    }
  ],
  "command_integrations": [],
  "menu": {
    "start_command": "/start",
    "rows": [
      [
        {
          "text": "Status",
          "builtin": "status"
        }
      ],
      [
        {
          "text": "Leaks",
          "builtin": "alarms"
        },
        {
          "text": "Batteries",
          "builtin": "batteries"
        }
      ],
      [
        {
          "text": "Away",
          "device_id": "home_mode",
          "action": "on"
        },
        {
          "text": "Home",
          "device_id": "home_mode",
          "action": "off"
        }
      ],
      [
        {
          "text": "Close water",
          "device_id": "water_valve",
          "action": "off"
        },
        {
          "text": "Open water",
          "device_id": "water_valve",
          "action": "on"
        }
      ],
      [
        {
          "text": "Siren ON",
          "device_id": "siren",
          "action": "on"
        },
        {
          "text": "Silence siren",
          "device_id": "siren",
          "action": "off"
        }
      ]
    ]
  },
  "events": [
    {
      "id": "toilet_leak",
      "on_topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}",
      "device_id": "leak_toilet",
      "updates": {
        "water_leak": "payload.water_leak",
        "battery": "payload.battery"
      },
      "when": {
        "water_leak": true
      },
      "allow_retained": true,
      "actions": [
        "water_alarm.trigger"
      ],
      "notify": {
        "users": "all",
        "text": "Water leak: {{.Device.Name}}. MQTT commands completed: {{.ActionsOK}}"
      }
    },
    {
      "id": "valve_state",
      "on_topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}",
      "device_id": "water_valve",
      "updates": {
        "state": "eq(payload.state,'ON')"
      }
    },
    {
      "id": "siren_state",
      "on_topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}",
      "device_id": "siren",
      "updates": {
        "state": "eq(payload.alarm,'true')"
      }
    },
    {
      "id": "weather_state",
      "on_topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}",
      "device_id": "weather",
      "updates": {
        "temperature": "payload.temperature",
        "humidity": "payload.humidity",
        "pressure": "pressure_mmhg(payload.pressure)",
        "battery": "payload.battery"
      }
    },
    {
      "id": "light_state",
      "on_topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}",
      "device_id": "living_light",
      "updates": {
        "state": "eq(payload.state,'ON')"
      }
    },
    {
      "id": "button_single",
      "on_topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}",
      "device_id": "wall_button",
      "updates": {
        "battery": "payload.battery"
      },
      "when": {
        "action": "single"
      },
      "allow_retained": false,
      "actions": [
        "home_mode.on"
      ]
    },
    {
      "id": "button_double",
      "on_topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}",
      "device_id": "wall_button",
      "when": {
        "action": "double"
      },
      "allow_retained": false,
      "actions": [
        "siren.off"
      ]
    }
  ],
  "schedules": [
    {
      "id": "night_light_off",
      "at": "23:30",
      "action": "living_light.off"
    }
  ]
}
```

Copy the complete object to a file and validate it. Later examples are fragments, not objects to concatenate into that file.

The leak sensor's protective field is intentionally absent from `initial`. A dry report or previously saved known value is required before opening water.

## Connection and service settings

### Telegram

| Field | Type / default | Meaning |
| --- | --- | --- |
| `token` | Required nonempty string | Actual bot token. |
| `welcome_message` | String, empty | Reply to `/start`. |
| `allowed_users` | Array, empty | Authorized users. |
| `allowed_users[].id` | Integer | Telegram **user** ID, not username, phone number or group chat ID. |
| `allowed_users[].video_monitoring_enabled` | Boolean, `false` | Initial personal preference; persisted preferences take precedence. |
| `poll_timeout_sec` | Integer seconds, `60` | Long-poll timeout. |
| `http_timeout_sec` | Integer seconds, `70` | Must exceed the poll timeout. |
| `proxy.enabled` | Boolean, `false` | Enable proxy settings. |
| `proxy.type` | `none` / `socks5` | Direct connection or SOCKS5. MTProxy is not implemented. |
| `proxy.address` | String | SOCKS5 host:port, e.g. `127.0.0.1:1080`. |
| `proxy.username` / `password` | Strings | Optional proxy authentication. |
| `proxy.secret` | String | Reserved; no effect in supported modes. |

Authorization checks the message sender's ID. In a group, replies go to the group; video preferences belong to the sender. Notifications/videos go to configured users individually. Each user should start a private conversation with the bot first.

Obtain numeric user IDs from a trusted existing integration or incoming Telegram update data. Do not use the bot's own ID. Restart after changing the user list.

Example `telegram.proxy`:

```json
{"enabled":true,"type":"socks5","address":"127.0.0.1:1080","username":"","password":""}
```

This proxy affects Telegram only. MQTT automation runs while the bot connection is being retried.

### MQTT

| Field | Type / default | Meaning |
| --- | --- | --- |
| `server` | Required string | URI such as `tcp://192.168.1.2:1883`; include the port. |
| `login` / `password` | Strings, empty | Broker credentials. |
| `client_id` | String, `SmartHome` if empty | Unique for each simultaneously connected client. |
| `qos` | Integer 0/1/2; default 0 | Used for subscriptions and publications. Examples use 1. |
| `retained` | Boolean, `false` | Applies to all outgoing publications, including commands. Keep false for normal operation. |
| `base_topics` | String array; no automatic values | Topic names exposed to templates. Convention: bridge at index 0, voice integration at index 1. |
| `subscribe_to` | String array | Extra global subscriptions; templates receive `BaseTopic`. |

Automatic subscriptions cover `<base_topics[0]>/<zigbee_id>`, device `subscribe` entries, enabled integrations and explicit event topics. Global `zigbee2mqtt/#` is unnecessary for ordinary device rules.

Reconnect restores subscriptions. Publish/subscribe results are awaited for up to five seconds. A publish timeout can mean delivery is unknown.

QoS does not make local queues durable or confirm physical execution. There is no persistent offline event journal.

`mqtt.retained` affects outgoing messages. `events[].allow_retained` governs reactions to incoming retained reports. External integration commands always ignore retained input.

A leading slash is significant for the broker: `yandex/device` and `/yandex/device` differ. Subscribe to both if required.

### Storage and logging

| Field | Default | Meaning |
| --- | --- | --- |
| `storage.state_file` | `/var/lib/smarthome/state.json` | Runtime state. |
| `logging.level` | `info` | `debug`, `info`, `warn`/`warning` or `error`. |
| `logging.file` | Empty | Optional append-only log file. Parent must be accessible. |
| `logging.to_console` | False when a file is configured | Also log to console. With no file, console output is enabled automatically. |

Logs include timestamp, severity and calling source location. Under systemd, use `file:""` and console logging to let journald manage retention. File logging has no built-in rotation or reopen-on-SIGHUP support. Arrange appropriate external rotation, or stop, rotate and restart. Debug output can contain MQTT payloads.

Config/state/log must be distinct files. The validator detects direct conflicts and existing-file aliases it can identify.

### Display labels

| Field | Default / behavior |
| --- | --- |
| `off` / `on` | `ВЫКЛ` / `ВКЛ`. |
| `yes` / `no` | `ДА` / `НЕТ`. |
| `unknown_command` | `Неизвестная команда!`. |
| `datetime_format` | `02.01.06 15:04`; Go time layout. |
| `short_datetime_format` | Accepted but currently unused in display code. |

Set English labels explicitly. Some built-in messages remain Russian; labels do not translate all output.

Go layouts use the reference date `Mon Jan 2 15:04:05 MST 2006`. `2006-01-02 15:04:05` is an ISO-like example; `%Y-%m-%d` is not a Go layout.

## Devices and MQTT messages

| Field | Meaning |
| --- | --- |
| `id` | Internal stable ID used by references and saved state. |
| `name` | Display name. |
| `type` | Use `switch`, `sensor`, `alarm` or `virtual`; affects display/integration behavior. |
| `description` | Descriptive metadata. |
| `zigbee_id` | Bridge topic suffix: IEEE address or friendly name. |
| `yandex_id` | Optional external identifier. |
| `subscribe` | Additional device subscriptions. |
| `initial` | Default state map; applicable persisted values override defaults. |
| `actions` | Named actions. |
| `reports` | Derived values to publish during matching event processing. |
| `fields` | String metadata available to templates. |

`id` and `zigbee_id` are different. Preserve the logical ID when replacing hardware and update its topic suffix.

Typical report objects:

```json
{"state":"ON","linkquality":110}
```

```json
{"temperature":23.4,"humidity":48.2,"pressure":1013.25,"battery":87}
```

```json
{"water_leak":true,"battery":96}
```

For `lumi.weather`, confirm the bridge's pressure units. `pressure_mmhg` expects hPa and outputs rounded mmHg. Do not convert values already in mmHg.

For the supported Tuya-style siren, use actual booleans:

```json
{"alarm":true}
```

```json
{"alarm":false}
```

A light's `"state":"ON"` convention does not change a siren's boolean type. `TS0601` alone is insufficient identification: the manufacturer fingerprint, such as `_TZE200_t1blo2bj`, and the converter belong to the Zigbee bridge. Volume, melody and duration must match its supported writable properties. SmartHome does not implement Tuya datapoints.

A two-channel relay may require:

```json
{"state_l1":"OFF","state_l2":"OFF"}
```

Other models use `state_left`/`state_right`. Inspect real reports and device capabilities. Send only intended writable properties rather than an entire cached state object.

`last_seen` updates on live matched reports; retained snapshots do not refresh it. Initial placeholder timestamps and saved timestamps do not prove current connectivity.

## Actions, groups and protective guards

### Action fields

Actions are under `devices[].actions.<name>`. Names are configurable: `on`, `off`, `close`, `silence` and others.

| Field | Meaning |
| --- | --- |
| `title` | Human-readable result text. |
| `set_state` | Optional boolean target for local `state`. |
| `force` | Execute despite the target already matching local state; default false. |
| `publishes` | Ordered messages with `topic` and `payload`. |
| `run_actions` | Ordered `device.action` / `group.action` references. |
| `set_user_video` | Change the invoking Telegram user's video preference. |
| `block_when` | Protective conditions checked before execution. |

`set_state` suppresses duplicates unless forced. Local action state is optimistic; add event updates from real reports. On MQTT failure, rollback avoids overwriting newer state. A compound device action commits its parent state only after successful child actions. A chain can execute partially.

Use `force:true` for emergency close and siren commands so saved state does not suppress a required command. SmartHome does not create an automatic siren-off timer: duration is a device setting or must be controlled by configured commands.

Use `set_user_video` on a dedicated virtual device invoked from Telegram. Do not combine it with unrelated publishes/chains; a personal preference needs an invoking user.

For a device action, its publications run before its child actions. For an explicit group action, child actions run before the group’s publications. An error does not automatically cancel the remaining operations. Cyclic action references are rejected during validation.

String payloads are rendered text. Objects, arrays, numbers and booleans are JSON-encoded then rendered. Prefer objects for device commands.

### Protective opening guards

Fragment inside a water-opening action:

```json
{
"block_when": [
  {"device_id":"leak_toilet","field":"water_leak","value":true},
  {"device_id":"leak_bathroom","field":"water_leak","value":true}
]
}
```

Guard values must be booleans, strings or numbers, with consistent types for the same guarded field. Any matching condition blocks the action. Missing, unknown or incompatible protective values also block it. `force` never bypasses a guard.

`initial.water_leak:false` does not establish protective knowledge. After loading state, an appropriate saved value or report is needed. A battery-only report does not establish dryness. A dry report releases the sensor's guard.

A wet value persists until updated. If a sensor stops reporting while wet, opening remains blocked. Inspect/dry it and obtain a valid report. There is no protective-state maximum-age policy: persisted dry state is accepted without proving that the sensor is still online.

Place guards on **every relevant opening action**, not only on a menu button. Groups, schedules and integrations then use the same protection. Emergency closing and siren silence should remain unguarded.

### Groups

An implicit group action:

```json
{"id":"lights","name":"Lights","members":["hall_light","kitchen_light"]}
```

If the group has no explicit `off` action, `lights.off` calls `off` on every member. Each member must define it.

An explicit group action:

```json
{
  "id":"water_alarm",
  "name":"Water alarm",
  "members":[],
  "actions":{"trigger":{"run_actions":["water_valve.off","siren.on"]}}
}
```

Explicit actions execute their own `publishes`/`run_actions`; they do not automatically iterate `members`. Groups have no independent saved state. Shared action fields `set_state` and `set_user_video` are not executed for groups. Group publication templates receive `Group`.

Normal compound commands share an ordered in-memory queue. Emergency rules requiring a notification or boolean repeat suppression run outside that ordinary queue. They are not placed in the ordinary FIFO, but MQTT I/O, a busy device or a shared compound-action lock can still delay execution. Overflow and errors are logged, not durably retried.

## Events and expressions

### Event fields and processing order

| Field | Meaning |
| --- | --- |
| `id` | Unique event identifier. |
| `on_topic` | Topic/filter with MQTT wildcards and optional templates. Empty matches any message reaching the service. |
| `device_id` | State/report/template device context; required for updates. |
| `updates` | State field → expression map. |
| `when` | Incoming field → expected value; all conditions must match. |
| `allow_retained` | Permit actions/publications/notifications for retained input; default false. |
| `publishes` | Messages produced by a matching reaction. |
| `actions` | Named action references. |
| `notify` | Telegram notification: `users` and `text`. |

Notification `users` accepts `all` (also the default when empty) or `video_enabled`. Notification text is sent as plain text, without HTML parsing. The notification queue holds 256 recipient messages in memory; overflow or send failure is logged.

Events run in configuration order. State updates and device reports occur **before** `when` and retained-action checks. Therefore `when` restricts reactions, not updates.

For an event without `device_id`, use a literal topic: the runtime topic renderer does not provide a device/base-topic context in that case.

Conditions use the current payload, not accumulated state. Missing ordinary fields do not match. For scalar input use `$raw`:

```json
{"when":{"$raw":"ON"}}
```

Example momentary button rule:

```json
{
  "id":"button_double",
  "on_topic":"{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}",
  "device_id":"wall_button",
  "when":{"action":"double"},
  "actions":["siren.off"],
  "allow_retained":false
}
```

Actual button names depend on bridge/model: inspect whether it reports `double`, `single`, `hold` or channel-specific names.

The complete example combines leak updates and a `water_leak:true` reaction in one rule. A dry report updates state and rearms the rule.

Repeated successful boolean-true alarms are suppressed until an explicit contradictory report. Battery-only input does not reset the latch. Failed execution can retry on a subsequent matching report; no autonomous retry timer is created. Startup/reconnect permits the first matching alarm again.

Set `allow_retained:true` deliberately for retained wet reports. Keep it false for button actions. Retained messages can update state and produce reports even when reactions are disabled.

### Expression reference

`updates` and `reports[].expression` values are strings.

| Expression | Result |
| --- | --- |
| `payload.temperature` | Top-level JSON field. |
| `payload.battery` | Battery value. |
| `$raw` | Trimmed payload text. |
| `$raw_bool` | true/false, on/off, 1/0, ВКЛ/ВЫКЛ converted to boolean; unknown input produces no update. |
| `eq(payload.state,'ON')` | Equality; absent field produces no value. |
| `ne(payload.action,'')` | Inequality. |
| `and(eq(payload.state_l1,'OFF'),eq(payload.state_l2,'OFF'))` | Boolean AND of at least two boolean expressions. |
| `pressure_mmhg(payload.pressure)` | Rounded hPa / 1.333; missing/invalid input produces no update. |
| `manual` | Literal string constant. |

No arbitrary code, arithmetic syntax, nested traversal, `or` or `not` is implemented. `payload.a.b` addresses a literal top-level key `a.b`, not a nested object.

Outer whitespace is rejected; whitespace inside calls is allowed. Single quotes inside JSON strings simplify comparisons.

Live partial reports combine referenced fields within the current MQTT session. Separate relay-channel reports can resolve a compound expression. Missing operands do not invent false readings; a known false operand can resolve an AND. Cache resets on reconnect; retained snapshots are not mixed with live data.

Updates/reports that reference fields are evaluated only when the current message contains at least one referenced field. A battery-only report does not repeat an old button action.

## Templates and outgoing reports

Go `text/template` syntax is case-sensitive.

| Variable | Context |
| --- | --- |
| `{{index .BaseTopic 0}}` | Base topic array. |
| `{{.Device.ID}}` / `{{.Device.Name}}` | Device actions/reports/subscriptions and device-associated events. |
| `{{.Device.ZigbeeID}}` / `{{.Device.YandexID}}` | Bridge/external identifiers. |
| `{{index .Device.Fields "room"}}` | Custom metadata. |
| `{{.Group.ID}}` / `{{.Group.Name}}` | Explicit group publications. |
| `{{.Payload.temperature}}` | Event/report data; named actions do not inherit the triggering payload. |
| `{{.RawPayload}}` | Event publications and notifications. |
| `{{.Topic}}` / `{{.BoolPayload}}` | Event publications. |
| `{{.ActionsOK}}` | Notification execution result under the event's policy. |
| `{{json .Payload}}` | JSON encoding through the `json` helper. |

Global subscriptions receive only `BaseTopic`. Topic templates must render during configuration validation, so dynamic incoming fields cannot be used there. Dynamic payload/notification templates render at runtime and log missing-key/syntax errors.

Do not insert arbitrary unescaped strings into hand-built JSON. Use the `json` helper for proper encoding when constructing string payloads.

Example report, placed under a device's `reports`:

```json
{
  "name":"temperature",
  "topic":"/{{index .BaseTopic 1}}/{{.Device.YandexID}}/temperature",
  "expression":"payload.temperature"
}
```

At least one matching event must name that device with `device_id`. A report definition alone does not enable processing of every topic.

For a direct reading, use `field` instead of `expression`. Expression takes precedence if both exist. Computed values are scalar text, e.g. `23.4` or `true`, not an enclosing JSON object. `name` is metadata, not a state key; persist readings with `updates`.

## Telegram menu

`menu.rows` is an array of button rows. Choose one target per button:

- `builtin`: `status`, `alarms` or `batteries`.
- `device_id` with `action`.
- `group_id` with `action`.

```json
{
  "start_command":"/start",
  "rows":[
    [{"text":"Status","builtin":"status"}],
    [{"text":"Leaks","builtin":"alarms"},{"text":"Batteries","builtin":"batteries"}],
    [{"device_id":"water_valve","action":"off"},{"device_id":"water_valve","action":"on"}],
    [{"group_id":"water_alarm","action":"trigger"}]
  ]
}
```

`text` is a literal caption. Otherwise the caption is `Name - ON/OFF` using configured labels. Keep captions unique because incoming text selects the action.

`/start` always opens the keyboard. `start_command` adds an alternate exact command. Bot-name suffixes in group commands are not specially parsed.

`button_width` is accepted, default 20, but not used by the keyboard renderer.

Built-ins:

- **status**: switches with a `state` field, personal video setting, `home_mode` if present, and the first device having temperature, humidity and pressure.
- **alarms**: `type:"alarm"` devices' `water_leak` values, including unknown state.
- **batteries**: devices having a battery value. The button is shown if `battery` is declared in a device's `initial` or `fields`.

Status is local state, not an immediate radio poll.

## External command integrations

Example element of `command_integrations` for yandex2mqtt:

```json
{
  "id":"yandex",
  "enabled":true,
  "topics":[
    "/{{index .BaseTopic 1}}/{{.Device.YandexID}}",
    "{{index .BaseTopic 1}}/{{.Device.YandexID}}"
  ],
  "action_on":"on",
  "action_off":"off",
  "payload_on":["true","on","1","вкл"],
  "payload_off":["false","off","0","выкл"],
  "force":true
}
```

Add `"yandex"` as `base_topics[1]` and `yandex_id` to eligible devices. External topics/IDs must match the external application.

| Field | Behavior |
| --- | --- |
| `id` | Required identifier when enabled. |
| `enabled` | Default false. |
| `topics` | Topic templates; preferred over singular field. |
| `topic` | Single-template alternative when topics is absent. |
| `action_on` / `action_off` | Default `on` / `off`. |
| `payload_on` | Empty array defaults to true/on/1/вкл. |
| `payload_off` | Empty array defaults to false/off/0/выкл. |
| `force` | Bypasses target-state duplicate suppression, never protective guards. |

Inputs are scalar strings/booleans/numbers represented as text, matched case-insensitively after trimming whitespace/quotes. An object such as `{"state":"ON"}` is not a scalar command. Virtual devices are skipped, as are templates needing missing identifiers.

Retained integration commands are always ignored. Keep command publishers non-retained. Use separate state topics such as `/yandex/<id>/state`. Loop prevention can suppress publications for the originating integration during action execution; reports from devices are the appropriate actual-state path.

To disable this feature, use `"command_integrations":[]` and omit Yandex-specific fields/templates.

## Daily schedules

An element of `schedules`:

```json
{"id":"light_off_at_night","at":"23:30","action":"living_light.off"}
```

Uses server local time, 24-hour `HH:MM`. Once-per-day tracking is in memory. A restart during the matching minute can repeat execution. Missed times are not replayed; failed actions are logged without an automatic same-day retry.

Inspect Linux time with `timedatectl`. A service-specific timezone can be configured with `sudo systemctl edit smarthome`:

```ini
[Service]
Environment=TZ=Asia/Yekaterinburg
```

Then `sudo systemctl daemon-reload` and `sudo systemctl restart smarthome`. Ensure timezone data exists.

Cron expressions, weekdays, sunrise/sunset and per-schedule timezone settings are not implemented.

## Video monitoring

Put this under `video_monitoring`:

```json
{
  "enabled":true,
  "lock_file":"/var/lib/smarthome/video.save",
  "check_interval":"1s",
  "delete_lock_file":true
}
```

| Field | Meaning |
| --- | --- |
| `enabled` | Default false. |
| `lock_file` | Required when enabled; contains the path to a finished video, not video bytes. |
| `check_interval` | Go duration string, e.g. `500ms`/`1s`, or integer seconds; default one second. |
| `delete_lock_file` | True claims/removes trigger; false leaves it and deduplicates unchanged instances within the current process. |

Finish the video before producing the trigger. SmartHome needs read access to both files and parent directories; claiming/removing triggers also needs directory write access.

Single-producer atomic trigger example:

```bash
printf '%s\n' '/var/lib/smarthome/camera.mp4' > /var/lib/smarthome/video.save.tmp
mv /var/lib/smarthome/video.save.tmp /var/lib/smarthome/video.save
```

Run as an account with appropriate permissions. Temporary and final trigger must share a filesystem. One trigger path is not a multi-video queue: producers can overwrite an unprocessed trigger.

Personal recipient controls use a virtual device:

```json
{
  "id":"video_control",
  "name":"Video monitoring",
  "type":"virtual",
  "actions":{
    "on":{"set_user_video":true},
    "off":{"set_user_video":false}
  }
}
```

Add on/off menu buttons. Each allowed user's preference is independent and persisted; it overrides the config default.

Videos go to private user chats, not to the group containing the toggle. The implementation rejects files larger than `50 * 1024 * 1024` bytes and reads the file into memory.

With `delete_lock_file:true`, the trigger is removed after an attempt even on send failure or with no enabled recipients. The video itself is not deleted. Failed uploads are logged, not durably retried. With false, a restart can process the remaining trigger again.

## State, backup and updates

Example state shape:

```json
{
  "devices":{
    "leak_toilet":{"water_leak":false,"battery":94,"last_seen":1790000000},
    "water_valve":{"state":false,"last_seen":1790000000}
  },
  "users":{"111111111":{"video_monitoring_enabled":true}}
}
```

Keys are internal device IDs and decimal user-ID strings. Timestamps are illustrative.

Saves use a temporary file, sync and atomic rename. Do not edit while running. State includes optimistic targets as well as reported values.

| Startup condition | Behavior |
| --- | --- |
| State absent | Defaults; protective values stay unknown until appropriate reports. |
| Valid state | Load persisted values and user preferences. |
| Malformed state | Preserve damaged contents in a `.corrupt-*` copy, recover defaults, log/notify. |
| State unreadable or recovery fails | Continue without persistence; leave original untouched; log/notify. |

In the last case fix permissions/storage and restart to enable persistence. New reports can establish protective knowledge in memory, but it will not be saved during that process.

Before changing config or upgrading:

```bash
sudo systemctl stop smarthome
sudo install -d -m 0700 /root/smarthome-backup
sudo cp -a /etc/smarthome.conf /root/smarthome-backup/smarthome.conf
sudo cp -a /usr/local/bin/SmartHome /root/smarthome-backup/SmartHome
```

If state exists:

```bash
sudo cp -a /var/lib/smarthome/state.json /root/smarthome-backup/state.json
```

Install and check the intended executable:

```bash
sudo install -o root -g root -m 0755 ./SmartHome /usr/local/bin/SmartHome
sudo -u smarthome /usr/local/bin/SmartHome --check-config --config /etc/smarthome.conf
sudo systemctl start smarthome
sudo journalctl -u smarthome -n 100 --no-pager
```

Use the schema accepted by the installed binary. Do not restore stale dry readings as proof that a room is safe after a real leak.

The state file is **not** a Zigbee coordinator backup or pairing-key database. Back up the bridge/coordinator separately.

## Adding or replacing devices

To add:

1. Pair/interview through the Zigbee bridge.
2. Inspect actual MQTT topic, report keys and writable properties.
3. Add stable `id`, `name`, `type` and `zigbee_id`.
4. Add actions and event updates.
5. Add optional reports/integration IDs.
6. Add menu/group/schedule references.
7. For leak sensors, guard every relevant water-opening action.
8. Validate, restart and test the physical chain.

JSON changes need no SmartHome rebuild. Unsupported radio protocols still require Zigbee bridge support.

For replacement, retain the logical `id` and change `zigbee_id` and any model-dependent properties. Other logical references remain valid.

Old state is not proof of the new hardware's state. For a protective sensor, stop, back up state and remove only its obsolete protective fields if necessary, then obtain a new report. Unknown protection blocks opening. For actuators refresh reports and verify physical switching.

If changing internal IDs, update all references. State keys are not automatically migrated.

## Logs and troubleshooting

### Logs and basic checks

```bash
sudo journalctl -u smarthome -f
sudo journalctl -u smarthome --since '30 minutes ago' --no-pager
sudo tail -n 100 /var/log/smarthome/smarthome.log
```

The last command applies only when that log is configured. For diagnostics set `logging.level:debug`, validate and restart; return to info afterwards.

```bash
sudo -u smarthome /usr/local/bin/SmartHome --check-config --config /etc/smarthome.conf
sudo systemctl cat smarthome
sudo systemctl status smarthome --no-pager
sudo -u smarthome test -r /etc/smarthome.conf
sudo -u smarthome test -w /var/lib/smarthome
```

With Mosquitto tools and local anonymous broker access:

```bash
mosquitto_sub -h 127.0.0.1 -p 1883 -t 'zigbee2mqtt/#' -v
```

Use your actual host/authentication/TLS settings. Narrow subscriptions to the affected devices. Publish test commands only to verified actuator topics.

Standalone boolean siren test:

```bash
mosquitto_pub -h 127.0.0.1 -p 1883 -t 'zigbee2mqtt/SIREN_TOPIC_SUFFIX/set' -m '{"alarm":true}'
mosquitto_pub -h 127.0.0.1 -p 1883 -t 'zigbee2mqtt/SIREN_TOPIC_SUFFIX/set' -m '{"alarm":false}'
```

Replace the suffix with the actual device. These commands are non-retained.

### Common symptoms

| Symptom | Check |
| --- | --- |
| Help then exit | Add `--run`. |
| Required Telegram token | Correct config and a nonempty token. |
| Unknown field / JSON error | Spelling, comments, commas and types. |
| Invalid expression | Grammar, balanced quotes, no outer spaces. |
| Template index error | Referenced base-topic array index exists. |
| Restart loop | Validate as service user; inspect config and log access. |
| MQTT authorization/disconnection | Host, port, credentials, ACL, unique client ID. |
| Report has no reaction | Topic, event device ID, current payload keys, conditions and bridge report. |
| `alarm: must be true/false` | Actual JSON booleans and correct bridge converter. |
| Opening water refused | Guard is wet/unknown/missing; inspect reason and sensor reports. |
| MQTT succeeds but device does not act | Bridge logs, writable property/endpoint, radio and physical device. |
| Bot ignores messages | Sender's user ID, token, private bot start, network/proxy. |
| Wrong/duplicate menu behavior | Unique button captions; refresh with `/start` after restart. |
| Missing status/battery | Required fields and updates; display supports specific categories. |
| Stale last_seen | No live matched report; retained does not refresh time. |
| State not saved | Permissions, disk space, startup persistence warning; fix and restart. |
| Video missing | Watcher, enabled recipient, finished file, permissions, size and Telegram logs. |
| Queue full | Messages/actions can be lost; inspect load and slow MQTT operations. |
| No web interface | SmartHome has no web listener; use the bridge's separate panel. |

A protective refusal is not a broker failure. A broker acknowledgment is not proof of physical closure.

## Acceptance checks and operating limits

Check on actual equipment:

1. Every sensor's wet/dry report reaches the expected logical device.
2. Wet reports close intended valves and sound the siren.
3. Wet/unknown guards reject all opening routes.
4. Dry reports release the correct guard; they do not reopen water unless explicitly configured.
5. Double-click silences the siren without opening water.
6. Telegram, wall-button and optional voice actions target the correct equipment.
7. MQTT reconnect and restart do not replay momentary commands.
8. Retained wet reports run the intended rule when allowed.
9. State/log paths are accessible to the service user.
10. MQTT automation runs while Telegram is unavailable.

Limits:

- In-memory queues hold 1,024 incoming MQTT tasks, 64 waiting compound actions and 256 recipient notifications. One compound action can also be executing. Overflow or restart can lose work.
- No persistent outbox, guaranteed delivery or physical actuator acknowledgment.
- Guards use known local values without a freshness deadline or proof of sensor availability.
- Chains are ordered operations, not transactions; partial execution is not undone.
- Schedules/video are best-effort without durable catch-up.
- The bridge, broker, network and power must work; this is not an independent hardware protection controller.

For critical installations, verify physical behavior and use any independent safeguards required by the installation.

## Development and project layout

| Path | Purpose |
| --- | --- |
| `main.go` | CLI/startup. |
| `types.go`, `config.go` | JSON schema/validation. |
| `app.go`, `state_guards.go` | State and protective knowledge. |
| `mqtt.go`, `expressions.go` | Routing, events and expressions. |
| `actions.go`, `action_chains.go` | Actions, groups and compound queue. |
| `telegram.go`, `telegram_client.go` | Telegram UI/networking. |
| `render.go`, `logging.go` | Templates/logging. |
| `workers.go` | Schedules/video. |
| `configs/` | Examples. |
| `scripts/` | Installer/builds/integration check. |
| `systemd/` | Linux unit. |
| `bin/`, `SHA256SUMS` | Release binaries/checksums. |
| `*_test.go` | Regression tests. |

```bash
go mod verify
go vet ./...
go test ./...
go test -race ./...
go build -o SmartHome .
```

Race detection requires a supported native platform and C compiler/CGO. Cross-built binaries require the appropriate target environment to execute.

The MQTT integration script requires Python 3 and a runnable `bin/linux-amd64/SmartHome`. It starts a local MQTT emulator on a temporary port; it does not require Mosquitto or a real Telegram connection:

```bash
python3 scripts/test-mqtt-integration.py
```

Run it in a test environment, not against production actuators.

Release build:

```bash
bash scripts/build-release.sh
sha256sum -c SHA256SUMS
```

The script uses `CGO_ENABLED=0` for Linux AMD64/ARM64 and Windows AMD64. `GO_BINARY` selects the Go executable for that script.

Distributed under the [MIT license](LICENSE).

## Action completion notifications

Device actions and explicitly configured group actions accept an optional `notify` object with `users` (`all` or `video_enabled`) and a `text` template. Notifications are queued after the complete action chain finishes, regardless of whether the action originated from Telegram, an MQTT button event, or another supported entry point. `.ActionsOK` distinguishes successful command execution from a failed or partially blocked chain; it is not proof of physical actuation. Skipped "already executed" actions do not create another broadcast. MQTT event rules do not need modification.

Example field inside an action:

```json
"notify": {
  "users": "all",
  "text": "{{if .ActionsOK}}Away mode enabled.{{else}}Away mode could not be fully enabled. Check device states and the SmartHome log.{{end}}"
}
```

Preserve your current configuration, action chains, and state file when upgrading. See [NOTIFICATIONS.ru.md](NOTIFICATIONS.ru.md) for the Russian away-mode configuration guide. Telegram users must have started the bot and must not have blocked it.

## Video control for all users

Set `set_all_users_video` to `true` or `false` in a device action or an explicitly configured group action to switch video delivery for every current `telegram.allowed_users` recipient. No individual user IDs or caller identity are needed. For away-mode integration, add it to `home_mode.actions.on` and `home_mode.actions.off`, keeping existing action chains and notifications.

`python3 scripts/enable-away-video.py CURRENT_CONFIG NEW_CONFIG` creates a private copy with these settings and migrates recognized generated per-user commands from earlier configurations. Unrelated settings and externally referenced definitions are preserved. Validate the new file before installing it with permissions readable by your service account. See [AWAY_VIDEO.ru.md](AWAY_VIDEO.ru.md) for setup.

Switching happens at the start of execution, before device commands. Later failures do not roll it back; skipped already-executed actions do not switch again. This controls recipient settings, not camera power or recording. Individual `set_user_video` and `video_user_id` actions remain supported; do not combine personal and all-user settings in the same action.
