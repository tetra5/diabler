package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tetra5/diabler/pkg/d4/events"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	defaultUTCOffset    = 0
	defaultWBAlarmTimer = 0 // Minutes
	dataPath            = "./data/diabler.json"
	updateInterval      = 30 // Seconds
)

var mu sync.Mutex

type Data struct {
	Users []User `json:"diabler"`
	// const jsonStr = `
	// {
	// 	"diabler": [
	// 		{
	// 			"chat_id": "123456789",
	// 			"utc_offset": 0,
	// 			"wb_notify_period": 0,
	// 			"wb_notified_on": "2006-01-02T15:04:05Z",
	//			"menu_message_id": 0
	// 		}
	// 	]
	// }
	// `
}

type User struct {
	ChatID        string    `json:"chat_id"`
	UTCOffset     int       `json:"utc_offset,omitempty"`
	WBAlarmTimer  int       `json:"wb_alarm_timer,omitempty"`
	WBNotifiedOn  time.Time `json:"wb_notified_on,omitempty"`
	MenuMessageID int       `json:"menu_message_id,omitempty"`
}

func UpdateTimers(wbs *events.WorldBossSchedule, bot *tgbotapi.BotAPI) {
	data, err := LoadData(dataPath)
	if err != nil {
		log.Printf("Error loading data: %s", err)
	}
	wb := wbs.Next()
	for i, u := range data.Users {
		if u.WBAlarmTimer == 0 {
			continue
		}
		chatID, err := strconv.ParseInt(u.ChatID, 10, 64)
		if err != nil {
			log.Printf("Error parsing Chat ID %d: %s", u.ChatID, err)
			continue
		}
		remaining := time.Until(wb.SpawnTime)
		if remaining < time.Duration(u.WBAlarmTimer)*time.Minute+time.Duration(updateInterval*1.5)*time.Second {
			if u.WBNotifiedOn == wb.SpawnTime {
				continue
			}
			timerDuration := remaining - time.Duration(u.WBAlarmTimer)*time.Minute
			log.Printf("Setting %s timer for %q ...", timerDuration.String(), chatID)
			go MakeTimer(chatID, u.WBAlarmTimer, timerDuration, bot, wb)
			data.Users[i].WBNotifiedOn = wb.SpawnTime
			err = SaveData(dataPath, data)
			if err != nil {
				log.Printf("Error saving data: %s", err)
			}
		}
	}
}

func MakeTimer(chatID int64, alarmTime int, duration time.Duration, bot *tgbotapi.BotAPI, boss events.WorldBoss) {
	timer := time.NewTimer(duration)

	<-timer.C

	msg := tgbotapi.NewMessage(chatID, "")
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.Text = fmt.Sprintf(WBAlarmStr, boss.Name, PluralizeStr(alarmTime, "minute", "minutes", true))
	_, err := bot.Send(msg)
	if err != nil {
		log.Printf("Error sending message to Chat ID %d: %s", chatID, err)
	}
}

func LoadData(fPath string) (data *Data, err error) {
	mu.Lock()
	defer mu.Unlock()
	f, errOpenFile := os.OpenFile(fPath, os.O_CREATE|os.O_RDONLY, 0644)
	// log.Printf("Reading from %q ... ", fPath)
	bytes, errReadAll := io.ReadAll(f)
	// log.Printf("Read %d bytes", len(bytes))
	errUnmarshal := json.Unmarshal(bytes, &data)
	err = errors.Join(errOpenFile, errReadAll, errUnmarshal)
	return data, err
}

func SaveData(fPath string, d *Data) (err error) {
	mu.Lock()
	defer mu.Unlock()
	f, errOpenFile := os.OpenFile(fPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	bytes, errMarshal := json.Marshal(&d)
	log.Printf("Writing to %q ...", fPath)
	n, errWrite := f.Write(bytes)
	if errWrite != nil {
		return errWrite
	}
	log.Printf("Wrote %d bytes", n)
	err = errors.Join(errOpenFile, errMarshal, errWrite)
	return err
}

func GetUserIdx(d *Data, chatID int64) (idx int) {
	idx = -1
	for i, u := range d.Users {
		id, err := strconv.ParseInt(u.ChatID, 10, 64)
		if err != nil {
			return
		}
		if chatID == id {
			return i
		}
	}
	return idx
}

func NewUser(chatID int64) (user User) {
	return User{
		UTCOffset:     defaultUTCOffset,
		WBAlarmTimer:  defaultWBAlarmTimer,
		WBNotifiedOn:  time.Unix(0, 0),
		ChatID:        strconv.FormatInt(chatID, 10),
		MenuMessageID: 0,
	}
}

func main() {
	token := os.Getenv("TELEGRAM_TOKEN")
	if token == "" {
		log.Fatalf("TELEGRAM_TOKEN env var is missing")
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatalf("tgbotapi.NewBotAPI: %s", err)
	}
	timeout := 30
	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = timeout
	log.Printf("Telegram: @%s, update timeout %s", &bot.Self, PluralizeStr(timeout, "second", "seconds", true))

	wbs := events.NewWorldBossSchedule()

	ticker := time.NewTicker(time.Second * updateInterval)
	go func() {
		for {
			<-ticker.C
			go UpdateTimers(wbs, bot)
		}
	}()

	for update := range bot.GetUpdatesChan(updateConfig) {
		var chatID int64
		if update.Message != nil {
			chatID = update.Message.Chat.ID
		} else if update.CallbackQuery != nil {
			chatID = update.CallbackQuery.Message.Chat.ID
		} else {
			continue
		}

		msg := tgbotapi.NewMessage(chatID, "")
		msg.ParseMode = tgbotapi.ModeMarkdown

		// This flag decides if we should store message ID for the menu system to work properly.
		// Basically the "menu system" is just an ordinary chat message and there is an API call to
		// modify its contents being it text or markup (inline buttons) or both simultaneously.
		// Keeping that in mind not only we have to have this flag but also store the "menu" message ID
		// somewhere to keep things persistent.
		savingMessageID := false

		data, err := LoadData(dataPath)
		if err != nil {
			log.Printf("Error loading data: %s. I make a new one!", err)
			data = &Data{}
		}
		idx := GetUserIdx(data, chatID)
		if idx == -1 {
			log.Printf("User %d not found. I make a new one!", chatID)
			data.Users = append(data.Users, NewUser(chatID))
			idx = GetUserIdx(data, chatID)
			err := SaveData(dataPath, data)
			if err != nil {
				log.Printf("Error saving data: %s", err)
				msg.Text = DataSaveErrorStr
			}
		}
		utcOffset := data.Users[idx].UTCOffset
		fz := time.FixedZone("", 3600*utcOffset)

		// Handling inline menu callbacks
		if update.CallbackQuery != nil {
			callback := tgbotapi.NewCallback(update.CallbackQuery.ID, update.CallbackQuery.Data)
			_, err := bot.Request(callback)
			if err != nil {
				log.Printf("Error requesting callback: %s", err)
			}

			editMsg := tgbotapi.NewEditMessageTextAndMarkup(
				chatID,
				data.Users[idx].MenuMessageID,
				"",
				tgbotapi.NewInlineKeyboardMarkup(),
			)
			editMsg.ParseMode = tgbotapi.ModeMarkdown

			switch update.CallbackQuery.Data {
			//FIXME: Error editing "diabler-settings-time-offset-decrease" message: Too Many Requests: retry after 10
			case "diabler-wb":
				// Show next WB spawn time and alarm timer if set
				rounded := RoundUpTime(wbs.Next().SpawnTime, time.Minute)
				remaining := time.Until(rounded)
				msg.Text = fmt.Sprintf(wbNextSpawnTimeStr,
					wbs.Next().Name,
					remaining.Round(time.Second).String(),
					RoundUpTime(wbs.Next().SpawnTime.In(fz), time.Minute).Format(time.DateTime),
					FormatUTCOffset(utcOffset),
				)
				var timerStr string
				if data.Users[idx].WBAlarmTimer > 0 {
					timerStr = fmt.Sprintf(WBTimerStr, PluralizeStr(data.Users[idx].WBAlarmTimer, "minute", "minutes", true))
				} else {
					timerStr = WBTimerDisabledStr
				}
				msg.Text = strings.Join([]string{msg.Text, timerStr}, "\n")
			case "diabler-settings":
				textLines := []string{
					SettingsMenuStr,
					fmt.Sprintf(TimeOffsetStr, FormatUTCOffset(data.Users[idx].UTCOffset)),
				}
				if data.Users[idx].WBAlarmTimer == 0 {
					textLines = append(textLines, WBTimerDisabledMenuStr)
				} else {
					textLines = append(textLines, fmt.Sprintf(WBTimerMenuStr, PluralizeStr(data.Users[idx].WBAlarmTimer, "minute", "minutes", true)))
				}
				editMsg.Text = strings.Join(textLines, "\n")
				editMsg.ReplyMarkup = &settingsMenuMarkup
				_, err := bot.Send(editMsg)
				if err != nil {
					log.Printf("Error editing %q message: %s", "diabler-settings", err)
				}
			case "diabler-settings-time-offset":
				textLines := []string{
					SettingsMenuTimeOffsetStr,
					fmt.Sprintf(TimeOffsetStr, FormatUTCOffset(data.Users[idx].UTCOffset)),
				}
				editMsg.Text = strings.Join(textLines, "\n")
				editMsg.ReplyMarkup = &settingsTimeOffsetMenuMarkup
				_, err := bot.Send(editMsg)
				if err != nil {
					log.Printf("Error editing %q message: %s", "diabler-settings-time-offset", err)
				}
			case "diabler-settings-time-offset-decrease":
				data.Users[idx].UTCOffset -= 1
				SaveData(dataPath, data)
				data, _ = LoadData(dataPath)
				textLines := []string{
					SettingsMenuTimeOffsetStr,
					fmt.Sprintf(TimeOffsetStr, FormatUTCOffset(data.Users[idx].UTCOffset)),
				}
				editMsg.Text = strings.Join(textLines, "\n")
				editMsg.ReplyMarkup = &settingsTimeOffsetMenuMarkup
				_, err = bot.Send(editMsg)
				if err != nil {
					log.Printf("Error editing %q message: %s", "diabler-settings-time-offset-decrease", err)
				}
			case "diabler-settings-time-offset-increase":
				data.Users[idx].UTCOffset += 1
				// TODO: error handling
				SaveData(dataPath, data)
				data, _ = LoadData(dataPath)
				textLines := []string{
					SettingsMenuTimeOffsetStr,
					fmt.Sprintf(TimeOffsetStr, FormatUTCOffset(data.Users[idx].UTCOffset)),
				}
				editMsg.Text = strings.Join(textLines, "\n")
				editMsg.ReplyMarkup = &settingsTimeOffsetMenuMarkup
				_, err = bot.Send(editMsg)
				if err != nil {
					log.Printf("Error editing %q message: %s", "diabler-settings-time-offset-decrease", err)
				}
			case "diabler-settings-alarm":
				textLines := []string{
					SettingsMenuAlarmStr,
				}
				if data.Users[idx].WBAlarmTimer == 0 {
					textLines = append(textLines, WBTimerDisabledMenuStr)
				} else {
					textLines = append(textLines, fmt.Sprintf(WBTimerMenuStr, PluralizeStr(data.Users[idx].WBAlarmTimer, "minute", "minutes", true)))
				}
				editMsg.Text = strings.Join(textLines, "\n")
				editMsg.ReplyMarkup = &settingsAlarmMenuMarkup
				_, err = bot.Send(editMsg)
				if err != nil {
					log.Printf("Error editing %q message: %s", "diabler-settings-alarm", err)
				}
			case "diabler-settings-alarm-disable":
				data.Users[idx].WBAlarmTimer = 0
				data.Users[idx].WBNotifiedOn = time.Unix(0, 0)
				// TODO: error handling
				SaveData(dataPath, data)
				data, _ = LoadData(dataPath)
				textLines := []string{
					SettingsMenuAlarmStr,
				}
				if data.Users[idx].WBAlarmTimer == 0 {
					textLines = append(textLines, WBTimerDisabledMenuStr)
				} else {
					textLines = append(textLines, fmt.Sprintf(WBTimerMenuStr, PluralizeStr(data.Users[idx].WBAlarmTimer, "minute", "minutes", true)))
				}
				editMsg.Text = strings.Join(textLines, "\n")
				editMsg.ReplyMarkup = &settingsAlarmMenuMarkup
				_, err = bot.Send(editMsg)
				if err != nil {
					log.Printf("Error editing %q message: %s", "diabler-settings-alarm-reset", err)
				}
			case "diabler-main":
				editMsg.Text = MainMenuStr
				editMsg.ReplyMarkup = &mainMenuMarkup
				_, err := bot.Send(editMsg)
				if err != nil {
					log.Printf("Error editing %q message: %s", "diabler-main", err)
				}
			}

			// More callback handling
			// FIXME: Error editing "diabler-settings-alarm-decrease-" message: Bad Request: message is not modified:
			// specified new message content and reply markup are exactly the same as a current content and reply markup of the message
			if strings.HasPrefix(update.CallbackQuery.Data, "diabler-settings-alarm-decrease-") {
				minutes := ParseAlarmCallbackData(update.CallbackQuery.Data)
				if data.Users[idx].WBAlarmTimer-minutes >= 0 {
					data.Users[idx].WBAlarmTimer -= minutes
				} else {
					data.Users[idx].WBAlarmTimer = 0
				}
				data.Users[idx].WBNotifiedOn = time.Unix(0, 0)
				// TODO: error handling
				SaveData(dataPath, data)
				data, _ = LoadData(dataPath)
				textLines := []string{
					SettingsMenuAlarmStr,
				}
				if data.Users[idx].WBAlarmTimer == 0 {
					textLines = append(textLines, WBTimerDisabledMenuStr)
				} else {
					textLines = append(textLines, fmt.Sprintf(WBTimerMenuStr, PluralizeStr(data.Users[idx].WBAlarmTimer, "minute", "minutes", true)))
				}
				editMsg.Text = strings.Join(textLines, "\n")
				editMsg.ReplyMarkup = &settingsAlarmMenuMarkup
				_, err = bot.Send(editMsg)
				if err != nil {
					log.Printf("Error editing %q message: %s", "diabler-settings-alarm-decrease-", err)
				}
			}
			if strings.HasPrefix(update.CallbackQuery.Data, "diabler-settings-alarm-increase-") {
				minutes := ParseAlarmCallbackData(update.CallbackQuery.Data)
				data.Users[idx].WBAlarmTimer += minutes
				data.Users[idx].WBNotifiedOn = time.Unix(0, 0)
				// TODO: error handling
				SaveData(dataPath, data)
				data, _ = LoadData(dataPath)
				textLines := []string{
					SettingsMenuAlarmStr,
				}
				if data.Users[idx].WBAlarmTimer == 0 {
					textLines = append(textLines, WBTimerDisabledMenuStr)
				} else {
					textLines = append(textLines, fmt.Sprintf(WBTimerMenuStr, PluralizeStr(data.Users[idx].WBAlarmTimer, "minute", "minutes", true)))
				}
				editMsg.Text = strings.Join(textLines, "\n")
				editMsg.ReplyMarkup = &settingsAlarmMenuMarkup
				_, err = bot.Send(editMsg)
				if err != nil {
					log.Printf("Error editing %q message: %s", "diabler-settings-alarm-increase-", err)
				}
			}
		}

		if update.Message != nil {
			// Handling chat commands
			switch update.Message.Command() {
			case "diabler":
				savingMessageID = true
				msg.Text = MainMenuStr
				msg.ReplyMarkup = mainMenuMarkup
			default:
				continue
			}
		}

		if msg.Text == "" {
			continue
		}

		sentMsg, err := bot.Send(msg)
		if err != nil {
			log.Printf("Error sending message: %s", err)
		}
		if savingMessageID {
			data.Users[idx].MenuMessageID = sentMsg.MessageID
			err := SaveData(dataPath, data)
			if err != nil {
				log.Printf("Error saving Message ID: %s", err)
			}
		}
	}
}

func RoundUpTime(t time.Time, dur time.Duration) time.Time {
	rounded := t.Round(dur)
	if rounded.Before(t) {
		rounded = rounded.Add(dur)
	}
	return rounded
}

func FormatUTCOffset(offset int) string {
	offsetStr := strconv.Itoa(offset)
	if offset >= 0 {
		offsetStr = "+" + offsetStr
	}
	return "UTC" + offsetStr
}

func PluralizeStr(n int, singular string, plural string, includeN bool) (result string) {
	nStr := strconv.Itoa(n)
	lastDigit, _ := strconv.Atoi(nStr[len(nStr)-1:])
	isPlural := lastDigit != 1
	if isPlural {
		result = plural
	} else {
		result = singular
	}
	if includeN {
		result = fmt.Sprintf("%d %s", n, result)
	}
	return result
}

func ParseAlarmCallbackData(s string) (minutes int) {
	fields := strings.Split(s, "-")
	minutes, _ = strconv.Atoi(strings.Replace(fields[len(fields)-1], "m", "", -1))
	return minutes
}

const (
	wbNextSpawnTimeStr        = "*%s* | `%s`\n%s %s."
	DataSaveErrorStr          = "Error 37. Please try again later."
	WBTimerDisabledStr        = "Alarm | `Disabled`"
	WBTimerDisabledMenuStr    = "Alarm: `Disabled`"
	WBTimerStr                = "Alarm | `%s`"
	WBTimerMenuStr            = "Alarm: `%s`"
	WBAlarmStr                = "*%s* | `%s`"
	MainMenuStr               = "*Diabler*"
	SettingsMenuStr           = "*Diabler | Settings*"
	SettingsMenuTimeOffsetStr = "*Diabler | Settings | Time offset*"
	SettingsMenuAlarmStr      = "*Diabler | Settings | Alarm*"
	TimeOffsetStr             = "Time offset: `%s`"
)

var mainMenuMarkup = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("👿 Next World Boss", "diabler-wb"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("\u2699 Settings", "diabler-settings"),
	),
)

var settingsMenuMarkup = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🌎 Time offset", "diabler-settings-time-offset"),
		tgbotapi.NewInlineKeyboardButtonData("⏰ Alarm", "diabler-settings-alarm"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Main menu", "diabler-main"),
	),
)

var settingsTimeOffsetMenuMarkup = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("-1 hour", "diabler-settings-time-offset-decrease"),
		tgbotapi.NewInlineKeyboardButtonData("+1 hour", "diabler-settings-time-offset-increase"),
	),
	tgbotapi.NewInlineKeyboardRow(
		returnToSettingsButton,
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Main menu", "diabler-main"),
	),
)

var settingsAlarmMenuMarkup = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("-30", "diabler-settings-alarm-decrease-30m"),
		tgbotapi.NewInlineKeyboardButtonData("-5", "diabler-settings-alarm-decrease-5m"),
		tgbotapi.NewInlineKeyboardButtonData("-1", "diabler-settings-alarm-decrease-1m"),
		tgbotapi.NewInlineKeyboardButtonData("+1", "diabler-settings-alarm-increase-1m"),
		tgbotapi.NewInlineKeyboardButtonData("+5", "diabler-settings-alarm-increase-5m"),
		tgbotapi.NewInlineKeyboardButtonData("+30", "diabler-settings-alarm-increase-30m"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("❌ Disable", "diabler-settings-alarm-disable"),
	),
	tgbotapi.NewInlineKeyboardRow(
		returnToSettingsButton,
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Main menu", "diabler-main"),
	),
)

var returnToSettingsButton = tgbotapi.NewInlineKeyboardButtonData("⬅️ Return to Settings", "diabler-settings")
