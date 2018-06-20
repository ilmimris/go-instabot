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

	state        = make(map[string]int)
	edit_message = make(map[string]int)
	mutex        = &sync.Mutex{}
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

	UserID := viper.GetInt64("user.telegram.id")

	// for update := range updates {
	// read updated
	for { //update := range updates {
		select {
		case update := <-updates:
			// UserName := update.Message.From.UserName

			if int64(update.Message.From.ID) == UserID {
				// ChatID := update.Message.Chat.ID

				Text := update.Message.Text

				log.Printf("[%d] %s", UserID, Text)
				var reply string
				msg := tgbotapi.NewMessage(UserID, "")

				if Text == "/follow" {

					state["follow_cancel"] = 0
					if state["follow"] >= 0 {
						msg.Text = fmt.Sprintf("Follow in progress (%d%%)", state["follow"])
						if edit_message["follow"] > 0 {
							edit := tgbotapi.EditMessageTextConfig{
								BaseEdit: tgbotapi.BaseEdit{
									ChatID:    UserID,
									MessageID: edit_message["follow"],
								},
								Text: msg.Text,
							}
							bot.Send(edit)
						} else {
							msg_res, err := bot.Send(msg)
							if err == nil {
								edit_message["follow"] = msg_res.MessageID
							}
						}
					} else {
						state["follow"] = 0
						report = make(map[line]int)
						msg.Text = "Starting follow"
						msg_res, err := bot.Send(msg)
						if err == nil {
							edit_message["follow"] = msg_res.MessageID
						}
						follow_req <- *update.Message
					}
				} else if Text == "/unfollow" {
					state["unfollow_cancel"] = 0
					if state["unfollow"] >= 0 {
						msg.Text = fmt.Sprintf("Unfollow in progress (%d%%)", state["unfollow"])
						if edit_message["unfollow"] > 0 {
							edit := tgbotapi.EditMessageTextConfig{
								BaseEdit: tgbotapi.BaseEdit{
									ChatID:    UserID,
									MessageID: edit_message["unfollow"],
								},
								Text: msg.Text,
							}
							bot.Send(edit)
						} else {
							msg_res, err := bot.Send(msg)
							if err == nil {
								log.Print(msg_res, msg_res.MessageID)
								edit_message["unfollow"] = msg_res.MessageID
							}
						}
					} else {
						state["unfollow"] = 0
						msg.Text = "Starting unfollow"
						msg_res, err := bot.Send(msg)
						if err == nil {
							edit_message["unfollow"] = msg_res.MessageID
						}
						unfollow_req <- *update.Message
					}
				} else if Text == "/progress" {
					var unfollow_progress = "not started"
					if state["unfollow"] >= 0 {
						unfollow_progress = fmt.Sprintf("%d%% [%d/%d]", state["unfollow"], state["unfollow_current"], state["unfollow_all_count"])
					}
					var follow_progress = "not started"
					if state["follow"] >= 0 {
						follow_progress = fmt.Sprintf("%d%% [%d/%d]", state["follow"], state["follow_current"], state["follow_all_count"])
					}
					msg.Text = fmt.Sprintf("Unfollow — %s\nFollow — %s", unfollow_progress, follow_progress)
					msg_res, err := bot.Send(msg)
					if err != nil {
						edit_message["progress"] = msg_res.MessageID
					}
				} else if Text == "/cancelfollow" {
					mutex.Lock()
					state["follow_cancel"] = 1
					mutex.Unlock()
				} else if Text == "/cancelunfollow" {
					mutex.Lock()
					state["unfollow_cancel"] = 1
					mutex.Unlock()
				} else if reply != "" {
					msg.Text = reply
					bot.Send(msg)
				}
			}
		case resp := <-follow_res:
			if edit_message["follow"] > 0 {
				edit := tgbotapi.EditMessageTextConfig{
					BaseEdit: tgbotapi.BaseEdit{
						ChatID:    UserID,
						MessageID: edit_message["follow"],
					},
					Text: resp.body,
				}
				bot.Send(edit)
			} else {
				msg := tgbotapi.NewMessage(UserID, resp.body)
				msg.ReplyToMessageID = resp.m.MessageID
				bot.Send(msg)
			}
		case resp := <-unfollow_res:
			if edit_message["unfollow"] > 0 {
				edit := tgbotapi.EditMessageTextConfig{
					BaseEdit: tgbotapi.BaseEdit{
						ChatID:    UserID,
						MessageID: edit_message["unfollow"],
					},
					Text: resp.body,
				}
				bot.Send(edit)
			} else {
				msg := tgbotapi.NewMessage(UserID, resp.body)
				msg.ReplyToMessageID = resp.m.MessageID
				bot.Send(msg)
			}
		}
	}
}
