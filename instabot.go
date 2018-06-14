package main

import (
	"fmt"
	"log"

	"sync"

	"github.com/spf13/viper"
	"gopkg.in/telegram-bot-api.v4"
)

type FollowResponse struct {
	body string
	m    tgbotapi.Message
}

type UnfollowResponse struct {
	body string
	m    tgbotapi.Message
}

var (
	follow_req   chan tgbotapi.Message
	unfollow_req chan tgbotapi.Message

	follow_res   chan FollowResponse
	unfollow_res chan UnfollowResponse

	state = make(map[string]int)
	mutex = &sync.Mutex{}
)

func main() {
	state["follow"] = -1
	state["unfollow"] = -1

	// Gets the command line options
	parseOptions()
	// Gets the config
	getConfig()

	go login()
	// создаем канал
	follow_req = make(chan tgbotapi.Message, 5)
	go loopTags()
	follow_res = make(chan FollowResponse, 5)

	// создаем канал
	unfollow_req = make(chan tgbotapi.Message, 15)
	go syncFollowers()
	unfollow_res = make(chan UnfollowResponse, 15)

	bot, err := tgbotapi.NewBotAPI(viper.GetString("user.telegram.token"))
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false
	log.Printf("Authorized on account %s", bot.Self.UserName)

	// init chan
	var ucfg tgbotapi.UpdateConfig = tgbotapi.NewUpdate(0)
	ucfg.Timeout = 60

	updates, err := bot.GetUpdatesChan(ucfg)

	if err != nil {
		log.Fatalf("[INIT] [Failed to init Telegram updates chan: %v]", err)
	}

	// for update := range updates {
	// read updated
	for { //update := range updates {
		select {
		case update := <-updates:
			// UserName := update.Message.From.UserName
			UserID := int64(update.Message.From.ID)

			if int64(update.Message.From.ID) == viper.GetInt64("user.telegram.id") {
				// ChatID := update.Message.Chat.ID

				Text := update.Message.Text

				log.Printf("[%d] %s", UserID, Text)
				var reply string
				msg := tgbotapi.NewMessage(UserID, "")

				if Text == "/follow" {
					mutex.Lock()
					if state["follow"] >= 0 {
						mutex.Unlock()
						msg.Text = fmt.Sprintf("Follow in progress (%d%%)", state["follow"])
						bot.Send(msg)
					} else {
						state["follow"] = 0
						mutex.Unlock()
						follow_req <- *update.Message
						msg.Text = "Starting follow"
						bot.Send(msg)
					}
				} else if Text == "/unfollow" {
					mutex.Lock()
					if state["unfollow"] >= 0 {
						mutex.Unlock()
						msg.Text = fmt.Sprintf("Unfollow in progress (%d%%)", state["unfollow"])
						bot.Send(msg)
					} else {
						state["unfollow"] = 0
						mutex.Unlock()
						msg.Text = "Starting unfollow"
						bot.Send(msg)
						unfollow_req <- *update.Message
					}
				} else if reply != "" {
					msg.Text = reply
					bot.Send(msg)
				}
			}
		case resp := <-follow_res:
			msg := tgbotapi.NewMessage(resp.m.Chat.ID, resp.body)
			msg.ReplyToMessageID = resp.m.MessageID
			bot.Send(msg)
		case resp := <-unfollow_res:
			msg := tgbotapi.NewMessage(resp.m.Chat.ID, resp.body)
			msg.ReplyToMessageID = resp.m.MessageID
			bot.Send(msg)
		}

	}
}
