package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/boltdb/bolt"
	"github.com/fsnotify/fsnotify"
	"github.com/robfig/cron"
	"github.com/spf13/viper"
	"github.com/tevino/abool"
	"gopkg.in/telegram-bot-api.v4"
)

type TelegramResponse struct {
	body string
}

var (
	followRes          chan TelegramResponse
	unfollowRes        chan TelegramResponse
	followFollowersRes chan TelegramResponse

	state                    = make(map[string]int)
	editMessage              = make(map[string]map[int]int)
	likesToAccountPerSession = make(map[string]int)

	reportID      int64
	admins        []string
	telegramToken string
	instaUsername string
	instaPassword string

	commandKeyboard tgbotapi.ReplyKeyboardMarkup

	followIsStarted   = abool.New()
	unfollowIsStarted = abool.New()
	refollowIsStarted = abool.New()
)
var db *bolt.DB

func main() {
	editMessage["follow"] = make(map[int]int)
	editMessage["unfollow"] = make(map[int]int)
	editMessage["refollow"] = make(map[int]int)

	db, err := initBolt()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	c := cron.New()
	c.Start()
	defer c.Stop()

	go login()

	startFollowChan, _, _, stopFollowChan := followManager(db)
	followRes = make(chan TelegramResponse, 2)

	startUnfollowChan, _, _, stopUnfollowChan := unfollowManager(db)
	unfollowRes = make(chan TelegramResponse, 2)

	startRefollowChan, _, innerRefollowChan, stopRefollowChan := refollowManager(db)
	followFollowersRes = make(chan TelegramResponse, 2)

	bot, err := tgbotapi.NewBotAPI(telegramToken)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false
	log.Printf("Authorized on account %s", bot.Self.UserName)

	var ucfg tgbotapi.UpdateConfig = tgbotapi.NewUpdate(0)
	ucfg.Timeout = 60

	updates, err := bot.GetUpdatesChan(ucfg)

	if err != nil {
		log.Fatalf("[INIT] [Failed to init Telegram updates chan: %v]", err)
	}

	c.AddFunc("0 0 8 * * *", func() { fmt.Println("Start follow"); startFollow(bot, startFollowChan, reportID) })
	c.AddFunc("0 0 20 * * *", func() { fmt.Println("Start unfollow"); startUnfollow(bot, startUnfollowChan, reportID) })
	c.AddFunc("0 59 23 * * *", func() { fmt.Println("Send stats"); sendStats(bot, db, -1) })

	for _, task := range c.Entries() {
		log.Println(task.Next)
	}

	// read updated
	for { //update := range updates {
		select {
		case update := <-updates:
			// UserName := update.Message.From.UserName
			// log.Println(UserID)
			if intInStringSlice(int(update.Message.From.ID), admins) {
				// ChatID := update.Message.Chat.ID

				Text := update.Message.Text
				Command := update.Message.Command()
				Args := update.Message.CommandArguments()

				// log.Printf("[%d] %s, %s, %s", UserID, Text, Command, Args)

				msg := tgbotapi.NewMessage(int64(update.Message.From.ID), "")

				if Command == "refollow" {
					if Args == "" {
						msg.Text = fmt.Sprintf("/refollow username")
						bot.Send(msg)
					} else {
						startRefollow(bot, startRefollowChan, innerRefollowChan, int64(update.Message.From.ID), Args)
					}
				} else if Command == "follow" {
					startFollow(bot, startFollowChan, int64(update.Message.From.ID))
				} else if Command == "unfollow" {
					startUnfollow(bot, startUnfollowChan, int64(update.Message.From.ID))
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
						editMessage["progress"][update.Message.From.ID] = msgRes.MessageID
					}
				} else if Command == "cancelfollow" {
					if followIsStarted.IsSet() {
						stopFollowChan <- true
						// followRes <- TelegramResponse{"Following canceled"}
					}
				} else if Command == "cancelunfollow" {
					if unfollowIsStarted.IsSet() {
						stopUnfollowChan <- true
						// unfollowRes <- TelegramResponse{"Unfollowing canceled"}
					}
				} else if Command == "cancelrefollow" {
					if refollowIsStarted.IsSet() {
						stopRefollowChan <- true
						// followFollowersRes <- TelegramResponse{"Refollowing canceled"}
					}
				} else if Command == "stats" {
					sendStats(bot, db, int64(update.Message.From.ID))
				} else if Command == "getcomments" {
					sendComments(bot, int64(update.Message.From.ID))
				} else if Command == "addcomments" {
					addComments(bot, Args, int64(update.Message.From.ID))
				} else if Command == "gettags" {
					sendTags(bot, int64(update.Message.From.ID))
				} else if Command == "addtag" {
					addTag(bot, Args, int64(update.Message.From.ID))
				} else if Text != "" {
					msg.Text = Text
					msg.ReplyMarkup = commandKeyboard
					bot.Send(msg)
				}
			}
		case resp := <-followRes:
			log.Println(resp.body)
			if len(editMessage["follow"]) > 0 {
				for UserID, EditID := range editMessage["follow"] {
					edit := tgbotapi.EditMessageTextConfig{
						BaseEdit: tgbotapi.BaseEdit{
							ChatID:    int64(UserID),
							MessageID: EditID,
						},
						Text: resp.body,
					}
					bot.Send(edit)
				}
			} else {
				msg := tgbotapi.NewMessage(reportID, resp.body)
				msgRes, err := bot.Send(msg)
				if err == nil {
					editMessage["follow"][int(reportID)] = msgRes.MessageID
				}
			}
		case resp := <-unfollowRes:
			log.Println(resp.body)
			if len(editMessage["unfollow"]) > 0 {
				for UserID, EditID := range editMessage["unfollow"] {
					edit := tgbotapi.EditMessageTextConfig{
						BaseEdit: tgbotapi.BaseEdit{
							ChatID:    int64(UserID),
							MessageID: EditID,
						},
						Text: resp.body,
					}
					bot.Send(edit)
				}
			} else {
				msg := tgbotapi.NewMessage(reportID, resp.body)
				msgRes, err := bot.Send(msg)
				if err == nil {
					editMessage["unfollow"][int(reportID)] = msgRes.MessageID
				}
			}
		case resp := <-followFollowersRes:
			log.Println(resp.body)
			if len(editMessage["refollow"]) > 0 {
				for UserID, EditID := range editMessage["refollow"] {
					edit := tgbotapi.EditMessageTextConfig{
						BaseEdit: tgbotapi.BaseEdit{
							ChatID:    int64(UserID),
							MessageID: EditID,
						},
						Text: resp.body,
					}
					bot.Send(edit)
				}
			} else {
				msg := tgbotapi.NewMessage(reportID, resp.body)
				msgRes, err := bot.Send(msg)
				if err == nil {
					editMessage["refollow"][int(reportID)] = msgRes.MessageID
				}
			}
		}
	}
}

func startFollow(bot *tgbotapi.BotAPI, startChan chan bool, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	if followIsStarted.IsSet() {
		msg.Text = fmt.Sprintf("Follow in progress (%d%%)", state["follow"])
		if len(editMessage["follow"]) > 0 && intInStringSlice(int(UserID), GetKeys(editMessage["follow"])) {
			for UserID, EditID := range editMessage["follow"] {
				edit := tgbotapi.EditMessageTextConfig{
					BaseEdit: tgbotapi.BaseEdit{
						ChatID:    int64(UserID),
						MessageID: EditID,
					},
					Text: msg.Text,
				}
				bot.Send(edit)
			}
		} else {
			msgRes, err := bot.Send(msg)
			if err == nil {
				editMessage["follow"][int(UserID)] = msgRes.MessageID
			}
		}
	} else {
		startChan <- true
		msg.Text = "Starting follow"
		msgRes, err := bot.Send(msg)
		if err == nil {
			editMessage["follow"][int(UserID)] = msgRes.MessageID
		}
	}
}

func startUnfollow(bot *tgbotapi.BotAPI, startChan chan bool, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	if unfollowIsStarted.IsSet() {
		msg.Text = fmt.Sprintf("Unfollow in progress (%d%%)", state["unfollow"])
		if len(editMessage["unfollow"]) > 0 && intInStringSlice(int(UserID), GetKeys(editMessage["unfollow"])) {
			for UserID, EditID := range editMessage["unfollow"] {
				edit := tgbotapi.EditMessageTextConfig{
					BaseEdit: tgbotapi.BaseEdit{
						ChatID:    int64(UserID),
						MessageID: EditID,
					},
					Text: msg.Text,
				}
				bot.Send(edit)
			}
		} else {
			msgRes, err := bot.Send(msg)
			if err == nil {
				// log.Print(msgRes, msgRes.MessageID)
				editMessage["unfollow"][int(UserID)] = msgRes.MessageID
			}
		}
	} else {
		startChan <- true
		msg.Text = "Starting unfollow"
		fmt.Println(msg.Text)
		msgRes, err := bot.Send(msg)
		if err == nil {
			editMessage["unfollow"][int(UserID)] = msgRes.MessageID
		}
	}
}

func startRefollow(bot *tgbotapi.BotAPI, startChan chan bool, innerRefollowChan chan string, UserID int64, target string) {
	msg := tgbotapi.NewMessage(UserID, "")
	if refollowIsStarted.IsSet() {
		msg.Text = fmt.Sprintf("Refollow in progress (%d%%)", state["refollow"])
		if len(editMessage["refollow"]) > 0 && intInStringSlice(int(UserID), GetKeys(editMessage["refollow"])) {
			edit := tgbotapi.EditMessageTextConfig{
				BaseEdit: tgbotapi.BaseEdit{
					ChatID:    int64(UserID),
					MessageID: editMessage["refollow"][int(UserID)],
				},
				Text: msg.Text,
			}
			bot.Send(edit)
		} else {
			msgRes, err := bot.Send(msg)
			if err == nil {
				editMessage["refollow"][int(UserID)] = msgRes.MessageID
			}
		}
	} else {
		startChan <- true
		msg.Text = "Starting refollow"
		msgRes, err := bot.Send(msg)
		if err == nil {
			editMessage["refollow"][int(UserID)] = msgRes.MessageID
		}
		innerRefollowChan <- target
	}
}

func sendStats(bot *tgbotapi.BotAPI, db *bolt.DB, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	unfollowCount, _ := getStats(db, "unfollow")
	followCount, _ := getStats(db, "follow")
	refollowCount, _ := getStats(db, "refollow")
	likeCount, _ := getStats(db, "like")
	commentCount, _ := getStats(db, "comment")
	if unfollowCount > 0 || followCount > 0 || refollowCount > 0 || likeCount > 0 || commentCount > 0 {
		msg.Text = fmt.Sprintf("Unfollowed: %d\nFollowed: %d\nRefollowed: %d\nLiked: %d\nCommented: %d", unfollowCount, followCount, refollowCount, likeCount, commentCount)
		if UserID == -1 {
			for UserID := range admins {
				msg.ChatID = int64(UserID)
				bot.Send(msg)
			}
		} else {
			bot.Send(msg)
		}
	}
}

func sendComments(bot *tgbotapi.BotAPI, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(commentsList) > 0 {
		msg.Text = strings.Join(commentsList, ", ")
	} else {
		msg.Text = "Comments is empty"
	}

	bot.Send(msg)
}

func addComments(bot *tgbotapi.BotAPI, comments string, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(comments) > 0 {
		newComments := strings.Split(comments, ", ")
		newComments = append(commentsList, newComments...)
		newComments = SliceUnique(newComments)
		viper.Set("comments", newComments)
		viper.WriteConfig()
		msg.Text = "Comments updated"
	} else {
		msg.Text = "Comments is empty"
	}

	bot.Send(msg)
}

func sendTags(bot *tgbotapi.BotAPI, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(tagsList) > 0 {
		keys := GetKeys(tagsList)
		msg.Text = strings.Join(keys, ", ")
	} else {
		msg.Text = "Tags is empty"
	}

	bot.Send(msg)
}

func addTag(bot *tgbotapi.BotAPI, tag string, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	// if len(tags) > 0 {
	tag = strings.Replace(tag, ".", "", -1)
	if len(tag) > 0 {
		// 	for _, tag := range strings.Split(tags, ", ") {
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
		// }
		viper.WriteConfig()
		msg.Text = "Tag added"
	} else {
		msg.Text = "Tag is empty"
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
