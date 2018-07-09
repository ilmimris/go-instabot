package main

import (
	"fmt"
	"log"

	"sync"

	"github.com/boltdb/bolt"
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
	followReq   chan tgbotapi.Message
	unfollowReq chan tgbotapi.Message

	followRes   chan FollowResponse
	unfollowRes chan UnfollowResponse

	state       = make(map[string]int)
	editMessage = make(map[string]int)
	mutex       = &sync.Mutex{}
)
var db *bolt.DB

func main() {
	state["follow"] = -1
	state["unfollow"] = -1

	// Gets the command line options
	parseOptions()

	// Gets the config
	getConfig()

	db, err := initBolt()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	go login()
	// создаем канал
	followReq = make(chan tgbotapi.Message, 5)
	go loopTags(db)
	followRes = make(chan FollowResponse, 5)

	// создаем канал
	unfollowReq = make(chan tgbotapi.Message, 15)
	go syncFollowers(db)
	unfollowRes = make(chan UnfollowResponse, 15)

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
						if editMessage["follow"] > 0 {
							edit := tgbotapi.EditMessageTextConfig{
								BaseEdit: tgbotapi.BaseEdit{
									ChatID:    UserID,
									MessageID: editMessage["follow"],
								},
								Text: msg.Text,
							}
							bot.Send(edit)
						} else {
							msgRes, err := bot.Send(msg)
							if err == nil {
								editMessage["follow"] = msgRes.MessageID
							}
						}
					} else {
						state["follow"] = 0
						report = make(map[line]int)
						msg.Text = "Starting follow"
						msgRes, err := bot.Send(msg)
						if err == nil {
							editMessage["follow"] = msgRes.MessageID
						}
						followReq <- *update.Message
					}
				} else if Text == "/unfollow" {
					state["unfollow_cancel"] = 0
					if state["unfollow"] >= 0 {
						msg.Text = fmt.Sprintf("Unfollow in progress (%d%%)", state["unfollow"])
						if editMessage["unfollow"] > 0 {
							edit := tgbotapi.EditMessageTextConfig{
								BaseEdit: tgbotapi.BaseEdit{
									ChatID:    UserID,
									MessageID: editMessage["unfollow"],
								},
								Text: msg.Text,
							}
							bot.Send(edit)
						} else {
							msgRes, err := bot.Send(msg)
							if err == nil {
								log.Print(msgRes, msgRes.MessageID)
								editMessage["unfollow"] = msgRes.MessageID
							}
						}
					} else {
						state["unfollow"] = 0
						msg.Text = "Starting unfollow"
						msgRes, err := bot.Send(msg)
						if err == nil {
							editMessage["unfollow"] = msgRes.MessageID
						}
						unfollowReq <- *update.Message
					}
				} else if Text == "/progress" {
					var unfollowProgress = "not started"
					if state["unfollow"] >= 0 {
						unfollowProgress = fmt.Sprintf("%d%% [%d/%d]", state["unfollow"], state["unfollow_current"], state["unfollow_all_count"])
					}
					var followProgress = "not started"
					if state["follow"] >= 0 {
						followProgress = fmt.Sprintf("%d%% [%d/%d]", state["follow"], state["follow_current"], state["follow_all_count"])
					}
					msg.Text = fmt.Sprintf("Unfollow — %s\nFollow — %s", unfollowProgress, followProgress)
					msgRes, err := bot.Send(msg)
					if err != nil {
						editMessage["progress"] = msgRes.MessageID
					}
				} else if Text == "/cancelfollow" {
					mutex.Lock()
					state["follow_cancel"] = 1
					mutex.Unlock()
				} else if Text == "/cancelunfollow" {
					mutex.Lock()
					state["unfollow_cancel"] = 1
					mutex.Unlock()
				} else if Text == "/stats" {
					unfollowCount, _ := getStats(db, "unfollow")
					followCount, _ := getStats(db, "follow")
					likeCount, _ := getStats(db, "like")
					commentCount, _ := getStats(db, "comment")
					msg.Text = fmt.Sprintf("Unfollowed: %d\nFollowed: %d\nLiked: %d\nCommented: %d", unfollowCount, followCount, likeCount, commentCount)
					bot.Send(msg)
				} else if reply != "" {
					msg.Text = reply
					bot.Send(msg)
				}
			}
		case resp := <-followRes:
			if editMessage["follow"] > 0 {
				edit := tgbotapi.EditMessageTextConfig{
					BaseEdit: tgbotapi.BaseEdit{
						ChatID:    UserID,
						MessageID: editMessage["follow"],
					},
					Text: resp.body,
				}
				bot.Send(edit)
			} else {
				msg := tgbotapi.NewMessage(UserID, resp.body)
				msg.ReplyToMessageID = resp.m.MessageID
				bot.Send(msg)
			}
		case resp := <-unfollowRes:
			if editMessage["unfollow"] > 0 {
				edit := tgbotapi.EditMessageTextConfig{
					BaseEdit: tgbotapi.BaseEdit{
						ChatID:    UserID,
						MessageID: editMessage["unfollow"],
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
