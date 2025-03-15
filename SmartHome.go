package main

import (
    "os"
    "fmt"
    "math"
    "time"
    "io/ioutil"
    "strings"
    "encoding/json"
    "github.com/go-telegram-bot-api/telegram-bot-api"
    mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
    botToken string	= "TOKEN"
    rtsp string		= "rtsp://login:password@192.168.1.6:554/media/video1" // адрес RTSP потока для скриншотов
    video_lock string	= "/share/video.save" // Файл с ссылкой на последнее видео, говоряйщий о том, что появилось движение в камере
    MQTTServer string	= "tcp://192.168.1.2:1883"
    MQTTLogin string	= "login"
    MQTTPasswd string	= "password"
)

var (
    UsersID = [][]int64{{0000000000, 0}, {111111111, 0}, {2222222222, 0}} // Список пользователй, кому разрешено пользоваться ботом
    topic = [2] string{"zigbee2mqtt", "yandex"} // Топик в базе MQTT
    WoW = [2] string{"ВЫКЛ", "ВКЛ"} // Названия статусов
    // Идентификаторы датчиков затопления
    AlarmID = [][]any{
	{"0x00158d0006c50eb5", "Кухня под мойкой", false, 0},
	{"0x00158d0006c5d0d8", "Кладовка в нише у счетчиков", false, 0},
	{"0x00158d000725e347", "В туалете под унитазом", false, 0},
	{"0x00158d0006d394bd", "В туалете за зеркалом", false, 0},
	{"0x00158d00072b56e0", "Ванная у стиральной машины", false, 0},
	{"0x00158d0006d39e38", "Ванная под раковиной", false, 0},
	{"0x00158d0006d2e247", "Ванная в нише за унитазом", false, 0},
	{"0x00158d0006ca1faf", "Ванная в нише у счетчиков", false, 0}}

    // Идентификаторы устройств умного дома
    // {ИДЕНТИФИКАТОР ZIGBEE, ИДЕНТИФИКАТОР YANDEX, ОПИСАНИЕ, СОСТОЯНИЕ}
    // Датчик температуры, Температура, Влажность, Давление, Заряд батареи, Дата и вреся
    // МЫ УШЛИ, СОСТОЯНИЕ)
    // ВИДЕОКОНТРОЛЬ, СОСТОЯНИЕ)
    DeviceID = [][]any{
	{"0x540f57fffe8ec162", "Polotense01", "Полотенцесушитель", true},
	{"0x54ef441000376ad3", "Voda01", "Вода в ванной", true},
	{"0x54ef4410003aad6f", "Voda02", "Вода на кухне", true},
	{"0x00158d00073a6302", "Svet01", "Свет", true},
	{"0x00158d00073a89a6", "Plita01", "Электроплита", true, "Rozetki01", "Розетки", true},
	{"0xa4c13841d71f4d76", "Сирена"},
	{"0x00158d0006c58566", "Кнопка пришли/ушли", 0},
	{"0x00158d0006c5fa47", "Pogoda01", "Датчик температуры", 0.0, 0.0, 0.0, 0, "01-01-2025 00:00"},
	{"\"МЫ УШЛИ\"", false}}
	bot *tgbotapi.BotAPI
	client mqtt.Client
	err error
    LastAlarm = [] int64 {1735671600, 1735671600, 1735671600, 1735671600, 1735671600, 1735671600, 1735671600, 1735671600} // Последнее обновление устройств
    LastDevice = [] int64 {1735671600, 1735671600, 1735671600, 1735671600, 1735671600, 1735671600, 1735671600, 1735671600} // Последнее обновление устройств
)

// Переводим bool в string
func BtoS(Stat bool) string {
    if Stat { return "true" } else { return "false" }
}

// Переводим string в bool
func StoB(Stat string) bool {
    if Stat == "true" { return true } else { return false }
}

// Отправляем сообщение MQTT брокеру
func SendToMQTT(TopicID, MessText string) {
    if token := client.Publish(TopicID, 1, false, MessText); token.Wait() && token.Error() != nil {
	fmt.Printf("Ошибка отправки сообщения MQTT брокеру: %v", token.Error())
    }
}

// Включение/Выключение устройств черех MQTT запросы
func DevOnOff(Num int, Status bool) {
    switch Num {
    case 0: // Полотенцесушитель (включен / выключен)
	if Status {
	    SendToMQTT(topic[0]+"/"+DeviceID[Num][0].(string)+"/set", "{\"state\": \"ON\", \"power_outage_memory\": \"restore\"}")
	} else {
	    SendToMQTT(topic[0]+"/"+DeviceID[Num][0].(string)+"/set", "{\"state\": \"OFF\", \"power_outage_memory\": \"restore\"}")
	}

    case 1: // Вода на в ванной (включена / выключена)
        if Status {
	    SendToMQTT(topic[0]+"/"+DeviceID[Num][0].(string)+"/set", "{\"state_right\": \"OFF\", \"operation_mode_right\": \"decoupled\", \"state_left\": \"ON\", \"operation_mode_left\": \"decoupled\", \"flip_indicator_light\": \"OFF\"}")
	} else {
	    SendToMQTT(topic[0]+"/"+DeviceID[Num][0].(string)+"/set", "{\"state_left\": \"OFF\", \"operation_mode_left\": \"decoupled\", \"state_right\": \"ON\", \"operation_mode_right\": \"decoupled\", \"flip_indicator_light\": \"OFF\"}")
	}

    case 2: // Вода на кухне (включена / выключена)
	if Status {
	    SendToMQTT(topic[0]+"/"+DeviceID[Num][0].(string)+"/set", "{\"state_right\": \"OFF\", \"operation_mode_right\": \"decoupled\", \"state_left\": \"ON\", \"operation_mode_left\": \"decoupled\", \"flip_indicator_light\": \"OFF\"}")
	} else {
	    SendToMQTT(topic[0]+"/"+DeviceID[Num][0].(string)+"/set", "{\"state_left\": \"OFF\", \"operation_mode_left\": \"decoupled\", \"state_right\": \"ON\", \"operation_mode_right\": \"decoupled\", \"flip_indicator_light\": \"OFF\"}")
	}

    case 3: // Свет в ближних комнатах (включен / выключен)
	if Status {
	    SendToMQTT(topic[0]+"/"+DeviceID[Num][0].(string)+"/set", "{\"state_l1\": \"OFF\", \"state_l2\": \"OFF\"}")
	} else {
	    SendToMQTT(topic[0]+"/"+DeviceID[Num][0].(string)+"/set", "{\"state_l1\": \"ON\", \"state_l2\": \"ON\"}")
	}

    case 4: // Электроплита (включена / выключена)
	if Status {
	    SendToMQTT(topic[0]+"/"+DeviceID[Num][0].(string)+"/set", "{\"state_l1\": \"OFF\"}")
	} else {
	    SendToMQTT(topic[0]+"/"+DeviceID[Num][0].(string)+"/set", "{\"state_l1\": \"ON\"}")
	}

    case 41: // Розетки (включены / выключены)
	if Status {
	    SendToMQTT(topic[0]+"/"+DeviceID[4][0].(string)+"/set", "{\"state_l2\": \"OFF\"}")
	} else {
	    SendToMQTT(topic[0]+"/"+DeviceID[4][0].(string)+"/set", "{\"state_l2\": \"ON\"}")
	}

    case 5: // Включение и выключение Сирены
	if Status {
	    SendToMQTT(topic[0]+"/"+DeviceID[5][0].(string)+"/set", "{\"alarm\": 1, \"melody\": 8, \"volume\": \"medium\"}")
	} else {
	    SendToMQTT(topic[0]+"/"+DeviceID[5][0].(string)+"/set", "{\"alarm\": 0}")
	}
    }

    switch {
    case Num < 5:
	DeviceID[Num][3] = Status
	SendToMQTT("/"+topic[1]+"/"+DeviceID[Num][1].(string), BtoS(Status))
    case Num == 41:
	DeviceID[4][6] = Status
	SendToMQTT("/"+topic[1]+"/"+DeviceID[4][4].(string), BtoS(Status))
    }
}

// Включаем и выключаем режим "МЫ УШЛИ"
func HomeOff(Status bool) {
    DeviceID[8][1] = Status
    for y :=0; y < len(UsersID); y++ {
	if Status { UsersID[y][1] = 1 } else { UsersID[y][1] = 0 }
    }
    for y :=0; y < 5; y++ {
	go DevOnOff(y, !Status)
    }
    go DevOnOff(41, !Status)
}

// Функция проверки пользователя Telegram Bot по списку резрешенных пользователей
func isUserAllowed(userID int64) bool {
    for id :=0; id < len(UsersID); id++ {
	if userID == UsersID[id][0] {
	    return true
	}
    }
    return false
}

// Отрабатываем запросы от Telegram Bot
func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
    tmp := ""
    i := 0

    flg:=false
    for i = 0; i < 5; i ++ {
	if message.Text == fmt.Sprintf("%s\n%s", DeviceID[i][2].(string), WoW[0]) {
	    flg = true
	    if DeviceID[i][3].(bool) {
		DevOnOff(i, false)
		tmp = fmt.Sprintf("%s: <b>%s</b>", DeviceID[i][2], WoW[0])
	    } else {
		tmp = fmt.Sprintf("%s уже <b>%s</b>!", DeviceID[i][2], WoW[0])
	    }
	} else if message.Text == fmt.Sprintf("%s\n%s", DeviceID[i][2].(string), WoW[1]) {
	    flg = true
	    if !DeviceID[i][3].(bool) {
		DevOnOff(i, true)
		tmp = fmt.Sprintf("%s: <b>%s</b>", DeviceID[i][2], WoW[1])
	    } else {
		tmp = fmt.Sprintf("%s уже <b>%s</b>!", DeviceID[i][2], WoW[1])
	    }
	}
    }

    switch message.Text {
    case "/start":
	msg := tgbotapi.NewMessage(message.Chat.ID, "Добро пожаловать в умный дом квартиры Сутуловых!")
	keyboard := tgbotapi.NewReplyKeyboard(
	    tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(DeviceID[8][0].(string)+"\n"+WoW[0]),tgbotapi.NewKeyboardButton(DeviceID[8][0].(string)+"\n"+WoW[1])),
	    tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Видеоконтроль\n"+WoW[0]),tgbotapi.NewKeyboardButton("Видеоконтроль\n"+WoW[1])),
	    tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Статус умного дома")),
	    tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton("Затопления"),tgbotapi.NewKeyboardButton("Заряд батареек")),
	    tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(DeviceID[0][2].(string)+"\n"+WoW[0]),tgbotapi.NewKeyboardButton(DeviceID[0][2].(string)+"\n"+WoW[1])),
	    tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(DeviceID[1][2].(string)+"\n"+WoW[0]),tgbotapi.NewKeyboardButton(DeviceID[1][2].(string)+"\n"+WoW[1])),
	    tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(DeviceID[2][2].(string)+"\n"+WoW[0]),tgbotapi.NewKeyboardButton(DeviceID[2][2].(string)+"\n"+WoW[1])),
	    tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(DeviceID[3][2].(string)+"\n"+WoW[0]),tgbotapi.NewKeyboardButton(DeviceID[3][2].(string)+"\n"+WoW[1])),
	    tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(DeviceID[4][2].(string)+"\n"+WoW[0]),tgbotapi.NewKeyboardButton(DeviceID[4][2].(string)+"\n"+WoW[1])),
	    tgbotapi.NewKeyboardButtonRow(tgbotapi.NewKeyboardButton(DeviceID[4][5].(string)+"\n"+WoW[0]),tgbotapi.NewKeyboardButton(DeviceID[4][5].(string)+"\n"+WoW[1])),
	)
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
	break

    case fmt.Sprintf("%s\n%s", DeviceID[4][5].(string), WoW[0]):
	if DeviceID[4][6].(bool) {
	    DevOnOff(41, false)
	    tmp = fmt.Sprintf("%s: <b>%s</b>", DeviceID[4][5], WoW[0])
	} else {
	    tmp = fmt.Sprintf("%s уже <b>%s</b>!", DeviceID[4][5], WoW[0])
	}

    case fmt.Sprintf("%s\n%s", DeviceID[4][5].(string), WoW[1]):
	if !DeviceID[4][6].(bool) {
	    DevOnOff(41, true)
	    tmp = fmt.Sprintf("%s: <b>%s</b>", DeviceID[4][5], WoW[1])
	} else {
	    tmp = fmt.Sprintf("%s уже <b>%s</b>!", DeviceID[4][5], WoW[1])
	}

    case fmt.Sprintf("%s\n%s", DeviceID[8][0].(string), WoW[0]):
	if DeviceID[8][1].(bool) {
	    HomeOff(false)
	    tmp = fmt.Sprintf("%s: <b>%s</b>!", DeviceID[8][0], WoW[0])
	} else {
	    tmp = fmt.Sprintf("%s уже <b>%s</b>!", DeviceID[8][0], WoW[0])
	}

    case fmt.Sprintf("%s\n%s", DeviceID[8][0].(string), WoW[1]):
	if !DeviceID[8][1].(bool) {
	    HomeOff(true)
	    tmp = fmt.Sprintf("%s: <b>%s</b>!", DeviceID[8][0], WoW[1])
	} else {
	    tmp = fmt.Sprintf("%s уже <b>%s</b>!", DeviceID[8][0], WoW[1])
	}

    case fmt.Sprintf("Видеоконтроль\n%s", WoW[0]):
	for id :=0; id < len(UsersID); id++ {
	    if message.Chat.ID == UsersID[id][0] {
		if UsersID[id][1] == 1 {
		    UsersID[id][1] = 0
		    tmp = fmt.Sprintf("Видеоконтроль: <b>%s</b>!", WoW[0])
		} else {
		    tmp = fmt.Sprintf("Видеоконтроль уже <b>%s</b>!", WoW[0])
		}
	    }
	}

    case fmt.Sprintf("Видеоконтроль\n%s", WoW[1]):
	for id :=0; id < len(UsersID); id++ {
	    if message.Chat.ID == UsersID[id][0] {
		if UsersID[id][1] == 0 {
		    UsersID[id][1] = 1
		    tmp = fmt.Sprintf("Видеоконтроль: <b>%s</b>!", WoW[1])
		} else {
		    tmp = fmt.Sprintf("Видеоконтроль уже <b>%s</b>!", WoW[1])
		}
	    }
	}

    case "Статус умного дома":
	tmp += "<pre>\n"
	tmp += fmt.Sprintf("<b>%-18s%v</b>\n", DeviceID[8][0], DeviceID[8][1].(bool))
	for i :=0; i < len(UsersID); i++ {
	    if message.Chat.ID == UsersID[i][0] {
		if UsersID[i][1] == 1 {
		    tmp += fmt.Sprintf("<b>Видеоконтроль     true</b>\n\n")
		} else {
		    tmp += fmt.Sprintf("<b>Видеоконтроль     false</b>\n\n")
		}
	    }
	}
	for i = 0; i < 5; i++ {
	    tmp += fmt.Sprintf("%-18s%-18s%v\n", DeviceID[i][2], time.Unix(LastDevice[i], 0).Format("02.01.06 15:04"), DeviceID[i][3].(bool))
	}
	tmp += fmt.Sprintf("%-18s%-18s%v\n\n", DeviceID[4][5], time.Unix(LastDevice[4], 0).Format("02.01.06 15:04"), DeviceID[4][6].(bool))
	tmp = strings.Replace(tmp, "false", WoW[0], -1)
	tmp = strings.Replace(tmp, "true", WoW[1], -1)
	tmp += fmt.Sprintf("На %s:\n", time.Unix(LastDevice[7], 0).Format("02.01.2006 15:04"))
	tmp += fmt.Sprintf("Температура       %v°С\nВлажность         %v%%\nДавление          %vмм.рт.ст.", DeviceID[7][3], DeviceID[7][4], DeviceID[7][5])
	tmp += "\n</pre>"

    case "Затопления":
	tmp += "<pre>\n"
	for i =0; i < len(AlarmID); i++ {
	    tmp += fmt.Sprintf("%-28s%v\n", AlarmID[i][1], AlarmID[i][2].(bool))
	}
	tmp = strings.Replace(tmp, "false", "НЕТ", -1)
	tmp = strings.Replace(tmp, "true", "ДА", -1)
	tmp += "\n</pre>\n"

    case "Заряд батареек":
	tmp += "<pre>\n"
	for i = 0; i < len(AlarmID); i++ {
           tmp += fmt.Sprintf("%-28s%-13s%v%%\n", AlarmID[i][1], time.Unix(LastAlarm[i], 0).Format("02.01 15:04"), AlarmID[i][3])
	}
	tmp += fmt.Sprintf("%-28s%-13s%v%%\n", DeviceID[6][1], time.Unix(LastDevice[6], 0).Format("02.01 15:04"), DeviceID[6][2])
	tmp += fmt.Sprintf("%-28s%-13s%v%%\n", DeviceID[7][2], time.Unix(LastDevice[7], 0).Format("02.01 15:04"), DeviceID[7][6])
	tmp += "\n</pre>\n"

    default:
	if !flg { tmp = "Неизвестная команда!" }
    }

    mesg := tgbotapi.NewMessage(message.Chat.ID, tmp)
    mesg.ParseMode = "HTML" // Решим HTML для возможности вывода результата с моношрифтом
    bot.Send(mesg)
}

//  Реакция на сработку одного из датчкиов затопления. Num - порядковый номер датчика
func Zatoplenie(Num int) {
    AlarmID[Num][2] = true
    tmp := "СРАБОТАЛ ДАТЧИК ЗАТОПЛЕНИЯ "
    ii := 0
    if Num < 2 {
	tmp += "НА КУХНЕ!\n\n"
	for ii = 0; ii < 2; ii++ {
	    tmp += AlarmID[ii][1].(string)+": "+BtoS(AlarmID[ii][2].(bool))+"\n"
	}
	DevOnOff(2, false)
	tmp += "\nВода на кухне перекрыта!"
    } else if Num < 4 {
	tmp += "В ТУАЛЕТЕ!\n\n"
	for ii = 2; ii < 4; ii++ {
	    tmp += AlarmID[ii][1].(string)+": "+BtoS(AlarmID[ii][2].(bool))+"\n"
	}
	DevOnOff(1, false)
	tmp += "\nВода в ванной перекрыта!"
    } else {
	tmp += "В ВАННОЙ!\n\n"
	for ii = 4; ii < len(AlarmID); ii++ {
	    tmp += AlarmID[ii][1].(string)+": "+BtoS(AlarmID[ii][2].(bool))+"\n"
	}
	DevOnOff(1, false)
	tmp += "\nВода в ванной перекрыта!"
    }
    tmp = strings.Replace(tmp, "false", "НЕТ", -1)
    tmp = strings.Replace(tmp, "true", "ДА", -1)
    for x :=0; x < len(UsersID); x++ { bot.Send(tgbotapi.NewMessage(UsersID[x][0], tmp)) }
    DevOnOff(5, true)
}

// Обработка сообщений от MQTT брокера
func on_message(client mqtt.Client, msg mqtt.Message) {
    DevID := strings.ReplaceAll(msg.Topic(), topic[0]+"/", "")
    DevID = strings.ReplaceAll(DevID, "/"+topic[1]+"/", "")

    //  Отрабатываем команды от Yandex2MQTT
    for i :=0; i < 5; i++ {
	if DevID == DeviceID[i][1].(string) && DeviceID[i][3].(bool) != StoB(string(msg.Payload())) { DevOnOff(i, StoB(string(msg.Payload()))) }
    }
    if DevID == DeviceID[4][4].(string) && DeviceID[4][6].(bool) != StoB(string(msg.Payload())) { DevOnOff(41, StoB(string(msg.Payload()))) }

    var data struct {
	State string		`json:"state"`
	Temperature float32	`json:"temperature"`
	Humidity float32	`json:"humidity"`
	Pressure float32	`json:"pressure"`
	Battery float32		`json:"battery"`
	State_l1 string		`json:"state_l1"`
	State_l2 string		`json:"state_l2"`
	State_left string	`json:"state_left"`
	Water_leak bool		`json:"water_leak"`
	Action string		`json:"action"`
    }

    if err := json.Unmarshal(msg.Payload(), &data); err == nil {

    // Датчик температуры
    if DevID == DeviceID[7][0].(string) {
	DeviceID[7][3] = data.Temperature
	DeviceID[7][4] = data.Humidity
	DeviceID[7][5] = math.Round(float64(data.Pressure)/1.333)
	DeviceID[7][6] = data.Battery
	LastDevice[7] = time.Now().Unix()
	if DeviceID[7][3].(float32) != 0 && DeviceID[7][4].(float32) != 0 {
	    SendToMQTT("/"+topic[1]+"/"+DeviceID[7][1].(string)+"/temperature", fmt.Sprintf("%f", DeviceID[7][3].(float32)))
	    SendToMQTT("/"+topic[1]+"/"+DeviceID[7][1].(string)+"/humidity", fmt.Sprintf("%f", DeviceID[7][4].(float32)))
	    SendToMQTT("/"+topic[1]+"/"+DeviceID[7][1].(string)+"/pressure", fmt.Sprintf("%f", DeviceID[7][5].(float64)))
	}
    }

    // Отработка сообщений от полотенцесушителя
    if DevID == DeviceID[0][0].(string) {
	if data.State  == "ON" && !DeviceID[0][3].(bool) { DeviceID[0][3] = true }
	if data.State  == "OFF" && DeviceID[0][3].(bool) { DeviceID[0][3] = false }
	SendToMQTT("/"+topic[1]+"/"+DeviceID[0][1].(string)+"/state", BtoS(DeviceID[0][3].(bool)))
	LastDevice[0] = time.Now().Unix()
    }

    // Реакция на сообщения от Вода в ванной
    if DevID == DeviceID[1][0].(string) {
	if data.Action == "single_left" { DevOnOff(1, true) }
	if data.Action == "single_right" { DevOnOff(1, false) }
	if data.State_left == "ON" && !DeviceID[1][3].(bool) { DeviceID[1][3] = true }
	if data.State_left == "OFF" && DeviceID[1][3].(bool) { DeviceID[1][3] = false }
	SendToMQTT("/"+topic[1]+"/"+DeviceID[1][1].(string)+"/state",BtoS(DeviceID[1][3].(bool)))
	LastDevice[1] = time.Now().Unix()
    }

    // Реакция на сообщения от Вода на кухне
    if DevID == DeviceID[2][0].(string) {
	if data.Action == "single_left" { DevOnOff(2, true) }
	if data.Action == "single_right" { DevOnOff(2, false) }
	if data.State_left == "ON" && !DeviceID[2][3].(bool) { DeviceID[2][3] = true }
	if data.State_left == "OFF" && DeviceID[2][3].(bool) { DeviceID[2][3] = false }
	SendToMQTT("/"+topic[1]+"/"+DeviceID[2][1].(string)+"/state",BtoS(DeviceID[2][3].(bool)))
	LastDevice[2] = time.Now().Unix()
    }

    // Отработка сообщений от Свет
     if DevID == DeviceID[3][0].(string) {
	if data.State_l1 == "OFF" && data.State_l2 == "OFF" && !DeviceID[3][3].(bool) { DeviceID[3][3] = true }
	if data.State_l1 == "ON" && data.State_l2 == "ON"  && DeviceID[3][3].(bool) { DeviceID[3][3] = false }
	SendToMQTT("/"+topic[1]+"/"+DeviceID[3][1].(string)+"/state",BtoS(DeviceID[3][3].(bool)))
	LastDevice[3] = time.Now().Unix()
    }

    // Реакция на сообщения от Плита и Розетки
    if DevID == DeviceID[4][0].(string) {
	if data.State_l1 == "OFF" && !DeviceID[4][3].(bool) { DeviceID[4][3] = true }
	if data.State_l1 == "ON" && DeviceID[4][3].(bool) { DeviceID[4][3] = false }
	if data.State_l2 == "OFF" && !DeviceID[4][6].(bool) { DeviceID[4][6] = true }
	if data.State_l2 == "ON" && DeviceID[4][6].(bool) { DeviceID[4][6] = false }
	SendToMQTT("/"+topic[1]+"/"+DeviceID[4][1].(string)+"/state",BtoS(DeviceID[4][3].(bool)))
	SendToMQTT("/"+topic[1]+"/"+DeviceID[4][5].(string)+"/state",BtoS(DeviceID[4][6].(bool)))
	LastDevice[4] = time.Now().Unix()
    }

    // Реакция на сообщения от кнопки ПРИШЛИ/УШЛИ
    if DevID == DeviceID[6][0].(string) {
	if data.Action == "single" { go HomeOff(false) }
	if data.Action == "double" { DevOnOff(5, false) }
	if data.Action == "hold" { go HomeOff(true) }
	DeviceID[6][2] = data.Battery
	LastDevice[6] = time.Now().Unix()
    }

    // Отработка сообщений от датчика затопления
    for i := 0; i < len(AlarmID); i++ {
	if DevID == AlarmID[i][0].(string) {
	    if data.Water_leak && !AlarmID[i][2].(bool) { go Zatoplenie(i) } else { AlarmID[i][2] = data.Water_leak }
	    AlarmID[i][3] = data.Battery
	    LastAlarm[i] = time.Now().Unix()
	}
    }
    }
}

func connectMQTT() mqtt.Client {
    opts := mqtt.NewClientOptions()
    opts.AddBroker(MQTTServer)
    opts.SetUsername(MQTTLogin)
    opts.SetPassword(MQTTPasswd)
    opts.SetClientID("SmartHome")
    opts.SetDefaultPublishHandler(on_message)
    opts.SetKeepAlive(0)
    opts.SetOrderMatters(false)
    opts.SetAutoReconnect(true)
    opts.SetConnectionLostHandler(func(client mqtt.Client, err error) {
	fmt.Printf("Connection lost: %v", err)
	for i := 1; ; i++ {
	    fmt.Println("Reconnecting attempt %d...", i)
	    if token := client.Connect(); token.Wait() && token.Error() == nil {
		fmt.Println("Reconnected successfully")
		return
	    }
	    time.Sleep(time.Duration(i*2) * time.Second) // Экспоненциальная задержка
	}
    })

    client := mqtt.NewClient(opts)
    if token := client.Connect(); token.Wait() && token.Error() != nil {
	fmt.Println("MQTT connection error: %v", token.Error())
    }
    return client
}

func subscribe(client mqtt.Client, tpc string) {
    if token := client.Subscribe(tpc, 0, nil); token.Wait() && token.Error() != nil {
	fmt.Printf("Ошибка подписки на %s: %v", tpc, token.Error())
    }
//    fmt.Printf("Подписка на: %s\n", tpc)
}

func main() {
    // Инициируем подключение к Telegram bot
    for {
	bot, err = tgbotapi.NewBotAPI(botToken)
	if err == nil {
	    break
	}
	fmt.Println("Ошибка подключения к Telegram bot, повторная попытка через 5 секунд. Ошибка: ", err)
	time.Sleep(5 * time.Second)
    }
    updateConfig := tgbotapi.NewUpdate(0)
    updateConfig.Timeout = 60
    updates, _ := bot.GetUpdatesChan(updateConfig)

    // Запуск горутины для обработки сообщений от Telegram Bot
    go func() {
	for update := range updates {
	    if update.Message == nil {
		time.Sleep(time.Second)
		continue
	    }
	    if !isUserAllowed(int64(update.Message.From.ID)) {
		time.Sleep(time.Second)
		continue
	    }
	    handleMessage(bot, update.Message)
	}
    }()

    // Подключаемся к MQTT брокеку
    client = connectMQTT()
    for i := 0; i < 8; i++ {
	subscribe(client, topic[0]+"/"+DeviceID[i][0].(string))
	if i < 5 { subscribe(client, "/"+topic[1]+"/"+DeviceID[i][1].(string)) }
    }
    subscribe(client, "/"+topic[1]+"/"+DeviceID[4][4].(string))
    for i := 0; i < len(AlarmID); i++ {
	subscribe(client, topic[0]+"/"+AlarmID[i][0].(string))
    }
//    defer client.Disconnect(250)

    // Запуск горутины для таймера включения и выключения полотенцесушителя
    go func() {
	for {
	    now := time.Now()
	    if now.Hour() == 14 && now.Minute() == 0 {
		DevOnOff(0, false)
		time.Sleep(time.Minute)
	    } else if now.Hour() == 0 && now.Minute() == 0 {
		DevOnOff(0, true)
		time.Sleep(time.Minute)
	    }
	    time.Sleep(time.Second)
	}
    }()

    // Запуск горутины по проверке файла от видеоконтроля. Если есть - считываем из него путь к видео, удаляем файл и отправляем видео по ссылке в Telegram bot
    go func() {
	for {
	    video, err := os.ReadFile(video_lock)
	    if err == nil {
		videoFile, err := ioutil.ReadFile(string(video))
		if err == nil {
		    for i := 0; i < len(UsersID); i++ {
			if UsersID[i][1] == 1 {
			    _, err = bot.Send(tgbotapi.NewVideoUpload(UsersID[i][0], tgbotapi.FileBytes{Bytes: videoFile}))
			    if err != nil {
				fmt.Println("Ошибка отправки видеоконтроля в Telegram bot:", err)
				continue
			    }
			}
		    }
		}
		os.Remove(video_lock)
	    }
	    time.Sleep(time.Second)
	}
    }()

    select {}
}
