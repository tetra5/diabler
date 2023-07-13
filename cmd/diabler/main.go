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

type Data struct {
	Users []User `json:"diabler"`
	// const jsonStr = `
	// {
	// 	"diabler": [
	// 		{
	// 			"chat_id": "123456789",
	// 			"utc_offset": 0,
	// 			"wb_notify_period": 0,
	// 			"wb_notified_on": "2006-01-02T15:04:05Z"
	// 		}
	// 	]
	// }
	// `
}

type User struct {
	ChatID       string    `json:"chat_id"`
	UTCOffset    int       `json:"utc_offset,omitempty"`
	WBAlarmTimer int       `json:"wb_alarm_timer,omitempty"`
	WBNotifiedOn time.Time `json:"wb_notified_on,omitempty"`
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
			log.Printf("Error parsing Chat ID %s: %s", u.ChatID, err)
			continue
		}
		remaining := time.Until(wb.SpawnTime)
		if remaining < time.Duration(u.WBAlarmTimer)*time.Minute+time.Duration(updateInterval)*time.Second {
			if u.WBNotifiedOn == wb.SpawnTime {
				continue
			}
			timerDuration := remaining - time.Duration(u.WBAlarmTimer)*time.Minute
			log.Printf("Setting %s timer for %d ...", timerDuration.String(), chatID)
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
		log.Printf("Error sending message: %s", err)
	}
}

func LoadData(fPath string) (data *Data, err error) {
	f, errOpenFile := os.OpenFile(fPath, os.O_CREATE|os.O_RDONLY, 0644)
	// log.Printf("Reading from %s ... ", fPath)
	bytes, errReadAll := io.ReadAll(f)
	// log.Printf("Read %d bytes", len(bytes))
	errUnmarshal := json.Unmarshal(bytes, &data)
	err = errors.Join(errOpenFile, errReadAll, errUnmarshal)
	return data, err
}

func SaveData(fPath string, d *Data) (err error) {
	f, errOpenFile := os.OpenFile(fPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	bytes, errMarshal := json.Marshal(&d)
	log.Printf("Writing to %s ...", fPath)
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
		UTCOffset:    defaultUTCOffset,
		WBAlarmTimer: defaultWBAlarmTimer,
		WBNotifiedOn: time.Unix(0, 0),
		ChatID:       strconv.FormatInt(chatID, 10),
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
		if update.Message == nil {
			continue
		}
		if !update.Message.IsCommand() {
			continue
		}

		msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")
		msg.ParseMode = tgbotapi.ModeMarkdown

		switch update.Message.Command() {
		case "diabler":
			fields := strings.Fields(update.Message.Text)
			if len(fields) < 2 {
				msg.Text = usageStr
			} else {
				data, err := LoadData(dataPath)
				if err != nil {
					log.Printf("Data load error: %s. I make a new one!", err)
					data = &Data{}
				}

				chatID := update.Message.Chat.ID
				idx := GetUserIdx(data, chatID)
				if idx == -1 {
					log.Printf("User %d not found. I make a new one!", chatID)
					data.Users = append(data.Users, NewUser(chatID))
					idx = GetUserIdx(data, chatID)
					err = SaveData(dataPath, data)
					if err != nil {
						log.Printf("Data save error: %s\n", err)
						msg.Text = DataSaveErrorStr
					}
				}
				utcOffset := data.Users[idx].UTCOffset
				fz := time.FixedZone("", 3600*utcOffset)

				switch fields[1] {
				case "wb":
					switch len(fields) {
					case 2:
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
					case 3:
						// Set or remove the alarm timer
						prevMinutes := data.Users[idx].WBAlarmTimer
						minutes, err := strconv.Atoi(fields[2])
						if err != nil || minutes < 0 {
							minutes = 0
						}
						data.Users[idx].WBAlarmTimer = minutes
						data.Users[idx].WBNotifiedOn = time.Unix(0, 0)
						err = SaveData(dataPath, data)
						if err != nil {
							log.Printf("Data save error: %s\n", err)
							msg.Text = DataSaveErrorStr
						}
						if minutes > 0 {
							msg.Text = fmt.Sprintf(WBTimerChangedStr,
								PluralizeStr(prevMinutes, "minute", "minutes", true),
								PluralizeStr(minutes, "minute", "minutes", true),
							)
						} else {
							msg.Text = WBTimerDisabledStr
						}
						log.Printf("WB alarm timer set: Chat ID=%s, time=%s", data.Users[idx].ChatID, PluralizeStr(minutes, "minute", "minutes", true))
					default:
						msg.Text = usageStr
					}

				case "utc":
					switch len(fields) {
					case 2:
						// Show current UTC offset
						msg.Text = fmt.Sprintf(UTCOffsetStr, FormatUTCOffset(utcOffset))
					case 3:
						// Set UTC offset
						prevOffset := data.Users[idx].UTCOffset
						utcOffset, err = strconv.Atoi(fields[2])
						if err != nil {
							utcOffset = 0
						}
						data.Users[idx].UTCOffset = utcOffset
						err = SaveData(dataPath, data)
						if err != nil {
							log.Printf("Data save error: %s", err)
							msg.Text = DataSaveErrorStr
						} else {
							msg.Text = fmt.Sprintf(UTCOffsetSetStr, FormatUTCOffset(prevOffset), FormatUTCOffset(utcOffset))
						}
					}
				default:
					msg.Text = usageStr
				}
			}
		default:
			continue
		}

		_, err = bot.Send(msg)
		if err != nil {
			log.Printf("Send message error: %s", err)
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

const (
	usageStr = `*Diabler Usage*

*/diabler utc* | Show your local UTC offset
*/diabler utc <hours>* | Set UTC offset to *<hours>*. Can be negative

*/diabler wb* | Show next World Boss
*/diabler wb <minutes>* | Set the alarm to *<minutes>* or *0* to disable 
`
	wbNextSpawnTimeStr = "*%s* | `%s`\n%s %s."
	UTCOffsetStr       = "Time Offset | `%s`\nUse `/diabler utc <hours>` command to change it."
	UTCOffsetSetStr    = "Time Offset | `%s` \u2794 `%s`"
	DataLoadErrorStr   = "Error 37. Please try again later."
	DataSaveErrorStr   = "Error 37. Please try again later."
	WBTimerDisabledStr = "Alarm | `Disabled`"
	WBTimerStr         = "Alarm | `%s`"
	WBTimerChangedStr  = "Alarm | `%s` \u2794 `%s`"
	WBAlarmStr         = "*%s* | `%s`"
)
