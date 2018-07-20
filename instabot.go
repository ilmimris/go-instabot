package main

import (
	"fmt"
	"log"
	"strconv"
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
				} else if Command == "removecomments" {
					removeComments(bot, Args, int64(update.Message.From.ID))
				} else if Command == "gettags" {
					sendTags(bot, int64(update.Message.From.ID))
				} else if Command == "addtags" {
					addTags(bot, Args, int64(update.Message.From.ID))
				} else if Command == "removetags" {
					removeTags(bot, Args, int64(update.Message.From.ID))
				} else if Command == "updatelimits" {
					updateLimits(bot, Args, int64(update.Message.From.ID))
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
			for _, id := range admins {
				UserID, _ = strconv.ParseInt(id, 10, 64)
				msg.ChatID = UserID
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

func removeComments(bot *tgbotapi.BotAPI, comments string, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(comments) > 0 {
		removeComments := strings.Split(comments, ", ")
		var newComments []string
		for _, comment := range commentsList {
			if stringInStringSlice(comment, removeComments) {

			} else {
				newComments = append(newComments, comment)
			}
		}
		newComments = SliceUnique(newComments)
		viper.Set("comments", newComments)
		viper.WriteConfig()
		msg.Text = "Comments removed"
	} else {
		msg.Text = "Comments is empty"
	}

	bot.Send(msg)
}

func sendTags(bot *tgbotapi.BotAPI, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(tagsList) > 0 {
		// keys := GetKeys(tagsList)
		// msg.Text = strings.Join(keys, ", ")
		msg.Text = strings.Join(tagsList, ", ")
	} else {
		msg.Text = "Tags is empty"
	}

	bot.Send(msg)
}

func addTags(bot *tgbotapi.BotAPI, tag string, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	// if len(tags) > 0 {
	tag = strings.Replace(tag, ".", "", -1)
	if len(tag) > 0 {
		newTags := strings.Split(tag, ", ")
		newTags = append(tagsList, newTags...)
		newTags = SliceUnique(newTags)
		viper.Set("tags", newTags)
		viper.WriteConfig()
		msg.Text = "Tags added"
	} else {
		msg.Text = "Tag is empty"
	}

	bot.Send(msg)
}

func removeTags(bot *tgbotapi.BotAPI, tags string, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(tags) > 0 {
		removeTags := strings.Split(tags, ", ")
		var newTags []string
		for _, tag := range tagsList {
			if stringInStringSlice(tag, removeTags) {

			} else {
				newTags = append(newTags, tag)
			}
		}
		newTags = SliceUnique(newTags)
		viper.Set("tags", newTags)
		viper.WriteConfig()
		msg.Text = "Tags removed"
	} else {
		msg.Text = "Tags is empty"
	}

	bot.Send(msg)
}

func updateLimits(bot *tgbotapi.BotAPI, limitStr string, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	s := strings.Split(limitStr, " ")
	limits := []string{"maxSync", "daysBeforeUnfollow", "max_likes_to_account_per_session", "maxRetry", "like.min", "like.count", "like.max", "follow.min", "follow.count", "follow.max", "comment.min", "comment.count", "comment.max"}
	if len(s) != 2 {
		msg.Text = "/updatelimits limitname integer\nlimitname maybe one of: " + strings.Join(limits, ", ")
	} else {
		limit, count := s[0], s[1]
		limitCount, _ := strconv.Atoi(count)
		if stringInStringSlice(limit, limits) {
			if limitCount >= 0 && limitCount <= 10000 {
				viper.Set("limits."+limit, limitCount)
				viper.WriteConfig()
				msg.Text = "Limit updated"
			} else {
				msg.Text = "/updatelimits limitname integer\ncount should be equal or greater than 0 and less or equal than 10000"
			}
		} else {
			msg.Text = "/updatelimits limitname integer\nlimitname maybe one of: " + strings.Join(limits, ", ")
		}
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
