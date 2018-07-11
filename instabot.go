package main

import (
	"fmt"
	"log"
	"strings"

	"sync"

	"github.com/boltdb/bolt"
	"github.com/fsnotify/fsnotify"
	"github.com/robfig/cron"
	"github.com/spf13/viper"
	"gopkg.in/telegram-bot-api.v4"
)

type TelegramResponse struct {
	body string
}

var (
	followReq          chan string
	unfollowReq        chan string
	followFollowersReq chan tgbotapi.Message

	followRes          chan TelegramResponse
	unfollowRes        chan TelegramResponse
	followFollowersRes chan TelegramResponse

	state       = make(map[string]int)
	editMessage = make(map[string]int)
	mutex       = &sync.Mutex{}

	UserID        int64
	telegramToken string
	instaUsername string
	instaPassword string

	commandKeyboard tgbotapi.ReplyKeyboardMarkup
)
var db *bolt.DB

func main() {
	state["follow"] = -1
	state["unfollow"] = -1
	state["refollow"] = -1

	db, err := initBolt()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	c := cron.New()
	c.Start()
	defer c.Stop()

	go login()
	// создаем канал
	followReq = make(chan string, 2)
	go loopTags(db)
	followRes = make(chan TelegramResponse, 2)

	// создаем канал
	unfollowReq = make(chan string, 2)
	go syncFollowers(db)
	unfollowRes = make(chan TelegramResponse, 2)

	// создаем канал
	followFollowersReq = make(chan tgbotapi.Message, 2)
	go followFollowers(db)
	followFollowersRes = make(chan TelegramResponse, 2)

	bot, err := tgbotapi.NewBotAPI(telegramToken)
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

	c.AddFunc("0 0 8 * * *", func() { fmt.Println("Start follow"); startFollow(bot) })
	c.AddFunc("0 0 20 * * *", func() { fmt.Println("Start unfollow"); startUnfollow(bot) })
	c.AddFunc("0 59 23 * * *", func() { fmt.Println("Send stats"); sendStats(bot, db) })

	for _, task := range c.Entries() {
		log.Println(task.Next)
	}

	// read updated
	for { //update := range updates {
		select {
		case update := <-updates:
			// UserName := update.Message.From.UserName
			// log.Println(UserID)
			if int64(update.Message.From.ID) == UserID {
				// ChatID := update.Message.Chat.ID

				Text := update.Message.Text
				Command := update.Message.Command()
				Args := update.Message.CommandArguments()

				// log.Printf("[%d] %s, %s, %s", UserID, Text, Command, Args)

				msg := tgbotapi.NewMessage(UserID, "")

				if Command == "refollow" {
					if Args == "" {
						msg.Text = fmt.Sprintf("/refollow username")
						bot.Send(msg)
					} else {
						state["refollow_cancel"] = 0
						if state["refollow"] >= 0 {
							msg.Text = fmt.Sprintf("Refollow in progress (%d%%)", state["refollow"])
							if editMessage["refollow"] > 0 {
								edit := tgbotapi.EditMessageTextConfig{
									BaseEdit: tgbotapi.BaseEdit{
										ChatID:    UserID,
										MessageID: editMessage["refollow"],
									},
									Text: msg.Text,
								}
								bot.Send(edit)
							} else {
								msgRes, err := bot.Send(msg)
								if err == nil {
									editMessage["refollow"] = msgRes.MessageID
								}
							}
						} else {
							state["refollow"] = 0
							report = make(map[line]int)
							msg.Text = "Starting refollow"
							msgRes, err := bot.Send(msg)
							if err == nil {
								editMessage["refollow"] = msgRes.MessageID
							}
							followFollowersReq <- *update.Message
						}
					}
				} else if Command == "follow" {
					startFollow(bot)
				} else if Command == "unfollow" {
					startUnfollow(bot)
				} else if Command == "progress" {
					var unfollowProgress = "not started"
					if state["unfollow"] >= 0 {
						unfollowProgress = fmt.Sprintf("%d%% [%d/%d]", state["unfollow"], state["unfollow_current"], state["unfollow_all_count"])
					}
					var followProgress = "not started"
					if state["follow"] >= 0 {
						followProgress = fmt.Sprintf("%d%% [%d/%d]", state["follow"], state["follow_current"], state["follow_all_count"])
					}
					var refollowProgress = "not started"
					if state["refollow"] >= 0 {
						refollowProgress = fmt.Sprintf("%d%% [%d/%d]", state["refollow"], state["refollow_current"], state["refollow_all_count"])
					}
					msg.Text = fmt.Sprintf("Unfollow — %s\nFollow — %s\nRefollow — %s", unfollowProgress, followProgress, refollowProgress)
					msgRes, err := bot.Send(msg)
					if err != nil {
						editMessage["progress"] = msgRes.MessageID
					}
				} else if Command == "cancelfollow" {
					mutex.Lock()
					state["follow_cancel"] = 1
					mutex.Unlock()
				} else if Command == "cancelunfollow" {
					mutex.Lock()
					state["unfollow_cancel"] = 1
					mutex.Unlock()
				} else if Command == "cancelrefollow" {
					mutex.Lock()
					state["refollow_cancel"] = 1
					mutex.Unlock()
				} else if Command == "stats" {
					sendStats(bot, db)
				} else if Command == "getcomments" {
					sendComments(bot)
				} else if Command == "updatecomments" {
					updateComments(bot, Args)
				} else if Command == "gettags" {
					sendTags(bot)
				} else if Command == "updatetags" {
					updateTags(bot, Args)
				} else if Text != "" {
					msg.Text = Text
					msg.ReplyMarkup = commandKeyboard
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
				bot.Send(msg)
			}
		case resp := <-followFollowersRes:
			if editMessage["refollow"] > 0 {
				edit := tgbotapi.EditMessageTextConfig{
					BaseEdit: tgbotapi.BaseEdit{
						ChatID:    UserID,
						MessageID: editMessage["refollow"],
					},
					Text: resp.body,
				}
				bot.Send(edit)
			} else {
				msg := tgbotapi.NewMessage(UserID, resp.body)
				bot.Send(msg)
			}
		}
	}
}

func startFollow(bot *tgbotapi.BotAPI) {
	msg := tgbotapi.NewMessage(UserID, "")
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
		followReq <- "Starting follow"
	}
}

func startUnfollow(bot *tgbotapi.BotAPI) {
	msg := tgbotapi.NewMessage(UserID, "")
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
		fmt.Println(msg.Text)
		msgRes, err := bot.Send(msg)
		if err == nil {
			editMessage["unfollow"] = msgRes.MessageID
		}

		unfollowReq <- msg.Text
	}
}

func sendStats(bot *tgbotapi.BotAPI, db *bolt.DB) {
	msg := tgbotapi.NewMessage(UserID, "")
	unfollowCount, _ := getStats(db, "unfollow")
	followCount, _ := getStats(db, "follow")
	refollowCount, _ := getStats(db, "refollow")
	likeCount, _ := getStats(db, "like")
	commentCount, _ := getStats(db, "comment")
	if unfollowCount > 0 || followCount > 0 || refollowCount > 0 || likeCount > 0 || commentCount > 0 {
		msg.Text = fmt.Sprintf("Unfollowed: %d\nFollowed: %d\nRefollowed: %d\nLiked: %d\nCommented: %d", unfollowCount, followCount, refollowCount, likeCount, commentCount)
		bot.Send(msg)
	}
}

func sendComments(bot *tgbotapi.BotAPI) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(commentsList) > 0 {
		msg.Text = strings.Join(commentsList, ", ")

	} else {
		msg.Text = "Comments is empty"
	}

	bot.Send(msg)
}

func updateComments(bot *tgbotapi.BotAPI, comments string) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(comments) > 0 {
		newComments := strings.Split(comments, ", ")
		viper.Set("comments", newComments)
		viper.WriteConfigAs("test.json")

		msg.Text = "Comments updated"
	} else {
		msg.Text = "Comments is empty"
	}

	bot.Send(msg)
}

func sendTags(bot *tgbotapi.BotAPI) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(tagsList) > 0 {
		keys := GetKeys(tagsList)
		msg.Text = strings.Join(keys, ", ")
	} else {
		msg.Text = "Tags is empty"
	}

	bot.Send(msg)
}

func updateTags(bot *tgbotapi.BotAPI, tags string) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(tags) > 0 {
		// newTags := strings.Split(tags, ", ")
		for _, tag := range strings.Split(tags, ", ") {
			key := "tags." + tag
			like := 20
			if viper.IsSet(key + ".like") {
				like = viper.GetInt(key + ".like")
			}
			viper.Set(key+".like", like)

			comment := 2
			if viper.IsSet(key + ".comment") {
				comment = viper.GetInt(key + ".comment")
			}
			viper.Set(key+".comment", comment)

			follow := 10
			if viper.IsSet(key + ".follow") {
				follow = viper.GetInt(key + ".follow")
			}
			viper.Set(key+".follow", follow)
		}
		viper.WriteConfig()
		msg.Text = "Tags updated"
	} else {
		msg.Text = "Tags is empty"
	}

	bot.Send(msg)
}

func init() {
	initKeyboard()
	parseOptions()
	getConfig()

	viper.WatchConfig()
	viper.OnConfigChange(func(e fsnotify.Event) {
		fmt.Println("Config file changed:", e.Name)
		getConfig()
	})
}

func initKeyboard() {
	commandKeyboard = tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("/stats"),
			tgbotapi.NewKeyboardButton("/progress"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("/follow"),
			tgbotapi.NewKeyboardButton("/unfollow"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("/cancelfollow"),
			tgbotapi.NewKeyboardButton("/cancelunfollow"),
			tgbotapi.NewKeyboardButton("/cancelrefollow"),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("/getcomments"),
			tgbotapi.NewKeyboardButton("/gettags"),
		),
	)
}
