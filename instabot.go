package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ad/cron"
	"github.com/ahmdrz/goinsta/v2"
	"github.com/boltdb/bolt"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"golang.org/x/net/proxy"

	tgbotapi "gopkg.in/telegram-bot-api.v4"
)

// MyInstabot is a wrapper around everything
type (
	MyInstabot struct {
		Insta *goinsta.Instagram
	}
	telegramResponse struct {
		body string
		key  string
	}
)

var (
	instabot MyInstabot

	telegramResp             chan telegramResponse
	state                    = make(map[string]int)
	editMessage              = make(map[string]map[int]int)
	likesToAccountPerSession = make(map[string]int)

	reportID int64
	admins   []string

	commandKeyboard       tgbotapi.ReplyKeyboardMarkup
	telegramToken         string
	telegramProxy         string
	telegramProxyPort     int32
	telegramProxyUser     string
	telegramProxyPassword string

	cronGeneralTask    int
	cronUpdateUnfollow int
	cronUnfollow       int
	cronStats          int
	cronLike           int

	instaUsername string
	instaPassword string
	instaProxy    string

	db *bolt.DB
	c  *cron.Cron
)

func main() {
	// Init connection to db
	db, err := initBolt()
	if err != nil {
		log.Println(err)
		return
	}
	defer db.Close()

	c := cron.New()
	c.Start()
	defer c.Stop()

	// Tries to login
	login()
	if unfollow {
		instabot.syncFollowers()
	} else if run {
		// Loop through tags ; follows, likes, and comments, according to the config file
		instabot.loopTags()
	}
	instabot.updateConfig()

	// Run telegrambot
	telegramResp = make(chan telegramResponse)

	// startFollowChan, _, _, stopFollowChan := followManager(db)
	startGeneralTaskChan, stopGeneralTaskChan := ControlManager("generalTask", func(name string) error { return startGeneralTask("generalTask", db) }, false)

	startUnfollowChan, stopUnfollowChan := ControlManager("unfollow", func(name string) error { return startUnFollowFromQueue("unfollow", db, 10) }, false)
	startUpdateUnfollowChan, stopUpdateUnfollowChan := ControlManager("updateunfollow", func(name string) error { return updateUnfollowList("updateunfollow", db) }, false)
	// startUnfollowChan, _, _, stopUnfollowChan := unfollowManager(db)

	var innerRefollowChan = make(chan string)
	startRefollowChan, stopRefollowChan := ControlManager("refollow", func(name string) error { return refollow("refollow", db, innerRefollowChan) }, false)
	// startRefollowChan, _, innerRefollowChan, stopRefollowChan := refollowManager(db)

	var innerfollowLikersChan = make(chan string)
	startfollowLikersChan, stopFollowLikersChan := ControlManager("followLikers", func(name string) error { return followLikers("followLikers", db, innerfollowLikersChan) }, false)

	var tr http.Transport

	if telegramProxy != "" {
		tr = http.Transport{
			DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				socksDialer, err := proxy.SOCKS5(
					"tcp",
					fmt.Sprintf("%s:%d", telegramProxy, telegramProxyPort),
					&proxy.Auth{User: telegramProxyUser, Password: telegramProxyPassword},
					proxy.Direct,
				)
				if err != nil {
					log.Println(err)
					return nil, err
				}

				return socksDialer.Dial(network, addr)
			},
		}
	}

	bot, err := tgbotapi.NewBotAPIWithClient(telegramToken, &http.Client{
		Transport: &tr,
	})

	if err != nil {
		log.Println(err)
		return
	}

	bot.Debug = false
	log.Printf("Authorized on account %s", bot.Self.UserName)

	msg := tgbotapi.NewMessage(int64(reportID), "Starting...")
	msg.DisableNotification = true
	bot.Send(msg)

	var ucfg = tgbotapi.NewUpdate(0)
	ucfg.Timeout = 60

	updates, err := bot.GetUpdatesChan(ucfg)
	if err != nil {
		log.Fatalf("[INIT] [Failed to init Telegram updates chan: %v]", err)
	}

	cronGeneralTask, _ = c.AddFunc("0 0 9 * * *", func() { fmt.Println("Start GeneralTask"); startGeneralTaskChan <- true })
	cronUpdateUnfollow, _ = c.AddFunc("0 0 1 * * *", func() { fmt.Println("Start updating unfollow list"); startUpdateUnfollowChan <- true })
	cronUnfollow, _ = c.AddFunc("0 0 * * * *", func() { fmt.Println("Start unfollow"); startUnfollowChan <- true })
	cronStats, _ = c.AddFunc("0 59 23 * * *", func() { fmt.Println("Send stats"); sendStats(bot, db, c, -1) })
	// cronLike, _ = c.AddFunc("0 30 10-21 * * *", func() { fmt.Println("Like followers"); likeFollowersPosts(db) })

	for _, task := range c.Entries() {
		log.Println(task.Next)
	}

	go func() {
		sigchan := make(chan os.Signal, 10)
		signal.Notify(sigchan, syscall.SIGTERM, syscall.SIGQUIT)
		<-sigchan

		msg := tgbotapi.NewMessage(int64(reportID), "Stopping...")
		msg.DisableNotification = true
		bot.Send(msg)
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}()

	for {
		select {
		case update := <-updates:
			if update.EditedMessage != nil {
				continue
			}

			if intInStringSlice(int(update.Message.From.ID), admins) {

				text := update.Message.Text
				command := update.Message.Command()
				args := update.Message.CommandArguments()

				msg := tgbotapi.NewMessage(int64(update.Message.From.ID), "")
				msg.DisableWebPagePreview = true
				msg.DisableNotification = true

				switch command {
				case "relogin":
					login()
					msg.Text = "relogin done"
					bot.Send(msg)
				case "refollow":
					if args == "" {
						msg.Text = "/refollow username"
						bot.Send(msg)
					} else {
						startRefollowChan <- true
						innerRefollowChan <- args
					}
				case "followlikers":
					if args == "" {
						msg.Text = "/followlikers post link"
						bot.Send(msg)
					} else {
						startfollowLikersChan <- true
						innerfollowLikersChan <- args
					}
				case "follow":
					startGeneralTaskChan <- true
				case "unfollow":
					startUnfollowChan <- true
				case "checkunfollow":
					startUpdateUnfollowChan <- true
				case "cancelcheckunfollow":
					stopUpdateUnfollowChan <- true
					// updateUnfollowList(db)
					// msg.Text = fmt.Sprintf("updateUnfollowList done")
					// bot.Send(msg)
				case "progress":
					// var unfollowProgress = "not started"
					// if state["unfollow"] >= 0 {
					// 	unfollowProgress = fmt.Sprintf("%d%% [%d/%d]", state["unfollow"], state["unfollow_current"], state["unfollow_all_count"])
					// }
					var followProgress = "not started"
					if state["follow"] >= 0 {
						followProgress = fmt.Sprintf("%d%% [%d/%d]", state["follow"], state["follow_current"], state["follow_all_count"])
					}
					var refollowProgress = "not started"
					if state["refollow"] >= 0 {
						refollowProgress = fmt.Sprintf("%d%% [%d/%d]", state["refollow"], state["refollow_current"], state["refollow_all_count"])
					}
					var followLikersProgress = "not started"
					if state["followLikers"] >= 0 {
						followLikersProgress = fmt.Sprintf("%d%% [%d/%d]", state["followLikers"], state["followLikers_current"], state["followLikers_all_count"])
					}
					msg.Text = fmt.Sprintf("Follow — %s\nRefollow — %s\nfollowLikers - %s", followProgress, refollowProgress, followLikersProgress)
					msgRes, err := bot.Send(msg)
					if err != nil {
						l.Lock()
						editMessage["progress"][update.Message.From.ID] = msgRes.MessageID
						l.Unlock()
					}
				case "cancelfollow":
					stopGeneralTaskChan <- true
				case "cancelunfollow":
					stopUnfollowChan <- true
				case "cancelrefollow":
					stopRefollowChan <- true
				case "cancelfollowlikers":
					stopFollowLikersChan <- true
				case "stats":
					sendStats(bot, db, c, int64(update.Message.From.ID))
				case "getcomments":
					sendComments(bot, int64(update.Message.From.ID))
				case "addcomments":
					addComments(bot, args, int64(update.Message.From.ID))
				case "removecomments":
					removeComments(bot, args, int64(update.Message.From.ID))
				case "gettags":
					sendTags(bot, int64(update.Message.From.ID))
				case "addtags":
					addTags(bot, args, int64(update.Message.From.ID))
				case "removetags":
					removeTags(bot, args, int64(update.Message.From.ID))
				case "getwhitelist":
					sendWhitelist(bot, int64(update.Message.From.ID))
				case "addwhitelist":
					addWhitelist(bot, args, int64(update.Message.From.ID))
				case "removewhitelist":
					removeWhitelist(bot, args, int64(update.Message.From.ID))
				case "getlimits":
					getLimits(bot, int64(update.Message.From.ID))
				case "updatelimits":
					updateLimits(bot, args, int64(update.Message.From.ID))
				case "updateproxy":
					updateProxy(bot, args, int64(update.Message.From.ID))
				case "like":
					likeFollowersPosts(db)
				default:
					msg.Text = text
					msg.ReplyMarkup = commandKeyboard
					bot.Send(msg)
				}
			}
		case resp := <-telegramResp:
			log.Println(resp.key, resp.body)
			if resp.key != "" {
				l.RLock()
				ln := len(editMessage[resp.key])
				l.RUnlock()
				if ln > 0 {
					l.RLock()
					rn := editMessage[resp.key]
					l.RUnlock()
					for UserID, EditID := range rn {
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
						l.Lock()
						editMessage[resp.key][int(reportID)] = msgRes.MessageID
						l.Unlock()
					}
				}
			}
		}
	}
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
