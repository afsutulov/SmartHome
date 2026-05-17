# SmartHome configuration guide

SmartHome reads JSON configuration from `/etc/smarthome.conf` by default.
Use another file with:

```bash
SmartHome --run --config ./smarthome.conf
```

## Top-level sections

| Section | Purpose |
| --- | --- |
| `telegram` | Telegram Bot API token, allowed users and optional proxy. |
| `mqtt` | MQTT broker connection and base topics. |
| `storage` | Runtime state file. |
| `logging` | Log level and log destination. |
| `labels` | Text labels shown to users. |
| `video_monitoring` | Optional trigger-file video notification. |
| `devices` | Device descriptions and actions. |
| `command_integrations` | External MQTT command sources, for example yandex2mqtt. |
| `groups` | Groups of devices and group actions. |
| `menu` | Telegram keyboard layout. |
| `events` | MQTT event rules. |
| `schedules` | Daily time-based actions. |

## Devices

Each device must have a stable `id`.
The `id` is used in events, schedules, groups and menu items.

Supported practical device types:

- `switch` — controllable on/off device;
- `sensor` — sensor data such as temperature, humidity, pressure, battery;
- `alarm` — leak or alarm sensor;
- `virtual` — software-only switch, for example user video monitoring.

Example switch:

```json
{
  "id": "living_room_light",
  "name": "Living room light",
  "type": "switch",
  "zigbee_id": "0x00124b0024abcdef",
  "yandex_id": "LivingRoomLight01",
  "initial": { "state": false },
  "actions": {
    "on": {
      "title": "ON",
      "set_state": true,
      "publishes": [
        { "topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}/set", "payload": { "state": "ON" } },
        { "topic": "/{{index .BaseTopic 1}}/{{.Device.YandexID}}/state", "payload": "true" }
      ]
    },
    "off": {
      "title": "OFF",
      "set_state": false,
      "publishes": [
        { "topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}/set", "payload": { "state": "OFF" } },
        { "topic": "/{{index .BaseTopic 1}}/{{.Device.YandexID}}/state", "payload": "false" }
      ]
    }
  }
}
```

## Template variables

Topic and payload templates can use:

| Variable | Meaning |
| --- | --- |
| `{{index .BaseTopic 0}}` | First base topic, usually `zigbee2mqtt`. |
| `{{index .BaseTopic 1}}` | Second base topic, usually `yandex`. |
| `{{.Device.ID}}` | Internal SmartHome device ID. |
| `{{.Device.ZigbeeID}}` | Zigbee/friendly name. |
| `{{.Device.YandexID}}` | yandex2mqtt device ID. |
| `{{index .Device.Fields "name"}}` | Custom field from `device.fields`. |

## Actions

Actions publish MQTT messages, update local state, run other actions or update user video settings.

Common fields:

- `title` — human-readable action title;
- `set_state` — local boolean state after action;
- `force` — execute even if local state already has the target value;
- `publishes` — MQTT messages;
- `run_actions` — chained actions like `device_id.action` or `group_id.action`;
- `set_user_video` — for virtual per-user video monitoring toggle.

## Events

Events are evaluated when MQTT messages arrive.

Example:

```json
{
  "id": "living_room_light_state",
  "on_topic": "{{index .BaseTopic 0}}/{{.Device.ZigbeeID}}",
  "device_id": "living_room_light",
  "updates": { "state": "eq(payload.state,'ON')" }
}
```

`updates` writes fields to runtime state. `when` can restrict event execution.

Supported expression examples:

- `payload.temperature`
- `payload.battery`
- `$raw`
- `$raw_bool`
- `eq(payload.state,'ON')`
- `ne(payload.action,'')`
- `and(eq(payload.state_l1,'OFF'),eq(payload.state_l2,'OFF'))`
- `pressure_mmhg(payload.pressure)`

## command_integrations

This section handles external MQTT command sources. The most common example is yandex2mqtt.

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

`force=true` is useful for voice assistants because local state may be stale.
SmartHome also skips publishing back into the same command topic to prevent loops.

## Groups

Groups reference device IDs:

```json
{
  "id": "all_lights",
  "name": "All lights",
  "members": ["living_room_light"]
}
```

You can call `all_lights.off` if member devices have an `off` action.

## Telegram menu

The Telegram keyboard is defined in rows:

```json
"menu": {
  "start_command": "/start",
  "rows": [
    [{ "text": "Smart home status", "builtin": "status" }],
    [{ "device_id": "living_room_light", "action": "off" }, { "device_id": "living_room_light", "action": "on" }]
  ]
}
```

Builtins:

- `status`
- `alarms`
- `batteries`

## Schedules

Schedules run once per day when local server time matches `at`.

```json
{ "id": "light_off_at_night", "at": "23:30", "action": "living_room_light.off" }
```
