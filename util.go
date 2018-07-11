package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/telegram-bot-api.v4"
)

// Whether we are in development mode or not
var dev *bool

// An image will be liked if the poster has more followers than likeLowerLimit, and less than likeUpperLimit
var likeLowerLimit int
var likeUpperLimit int

// A user will be followed if he has more followers than followLowerLimit, and less than followUpperLimit
// Needs to be a subset of the like interval
var followLowerLimit int
var followUpperLimit int

// An image will be commented if the poster has more followers than commentLowerLimit, and less than commentUpperLimit
// Needs to be a subset of the like interval
var commentLowerLimit int
var commentUpperLimit int

// Hashtags list. Do not put the '#' in the config file
var tagsList map[string]interface{}

// Limits for the current hashtag
var limits map[string]int

// Comments list
var commentsList []string

// Line is a struct to store one line of the report
type line struct {
	Tag, Action string
}

// Report that will be sent at the end of the script
var report map[line]int

// Counters that will be incremented while we like, comment, and follow
var numFollowed int
var numLiked int
var numCommented int

// Will hold the tag value
var tag string

// check will log.Fatal if err is an error
func check(err error) {
	if err != nil {
		log.Fatal("ERROR:", err)
	}
}

// Parses the options given to the script
func parseOptions() {
	dev = flag.Bool("dev", false, "Use this option to use the script in development mode : nothing will be done for real")
	logs := flag.Bool("logs", false, "Use this option to enable the logfile")

	flag.Parse()

	// -logs enables the log file
	if *logs {
		// Opens a log file
		t := time.Now()
		logFile, err := os.OpenFile("instabot-"+t.Format("2006-01-02-15-04-05")+".log", os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
		check(err)
		defer logFile.Close()

		// Duplicates the writer to stdout and logFile
		mw := io.MultiWriter(os.Stdout, logFile)
		log.SetOutput(mw)
	}
}

// Gets the conf in the config file
func getConfig() {
	folder := "config"
	if *dev {
		folder = "local"
	}
	viper.SetConfigFile(folder + "/config.json")

	// Reads the config file
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}

	// Confirms which config file is used
	log.Printf("Using config: %s\n\n", viper.ConfigFileUsed())

	likeLowerLimit = viper.GetInt("limits.like.min")
	likeUpperLimit = viper.GetInt("limits.like.max")

	followLowerLimit = viper.GetInt("limits.follow.min")
	followUpperLimit = viper.GetInt("limits.follow.max")

	commentLowerLimit = viper.GetInt("limits.comment.min")
	commentUpperLimit = viper.GetInt("limits.comment.max")

	tagsList = viper.GetStringMap("tags")

	commentsList = viper.GetStringSlice("comments")

	type Report struct {
		Tag, Action string
	}

	report = make(map[line]int)
}

// Sends an telegram. Check out the "telegram" section of the "config.json" file.
func send(body string, success bool) {
	bot, err := tgbotapi.NewBotAPI(viper.GetString("user.telegram.token"))
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false
	log.Printf("Authorized on account %s", bot.Self.UserName)

	msg := tgbotapi.NewMessage(viper.GetInt64("user.telegram.id"), body)
	bot.Send(msg)
}

// Retries the same function [function], a certain number of times (maxAttempts).
// It is exponential : the 1st time it will be (sleep), the 2nd time, (sleep) x 2, the 3rd time, (sleep) x 3, etc.
// If this function fails to recover after an error, it will send an email to the address in the config file.
func retry(maxAttempts int, sleep time.Duration, function func() error) (err error) {
	for currentAttempt := 0; currentAttempt < maxAttempts; currentAttempt++ {
		err = function()
		if err == nil {
			return
		}
		for i := 0; i <= currentAttempt; i++ {
			time.Sleep(sleep)
		}
		log.Println("Retrying after error:", err)
	}

	send(fmt.Sprintf("The script has stopped due to an unrecoverable error :\n%s", err), false)
	return fmt.Errorf("After %d attempts, last error: %s", maxAttempts, err)
}

// Builds the line for the report and prints it
func buildLine() {
	reportTag := ""
	for index, element := range report {
		if index.Tag == tag {
			reportTag += fmt.Sprintf("%s %d/%d - ", index.Action, element, limits[index.Action])
		}
	}
	// Prints the report line on the screen / in the log file
	if reportTag != "" {
		log.Println(strings.TrimSuffix(reportTag, " - "))
	}
}

func Shuffle(slice interface{}) {
	rv := reflect.ValueOf(slice)
	swap := reflect.Swapper(slice)
	length := rv.Len()
	for i := length - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		swap(i, j)
	}
}

func GetKeys(slice interface{}) []string {
	keys := reflect.ValueOf(slice).MapKeys()
	log.Println(keys)
	strkeys := make([]string, len(keys))
	for i := 0; i < len(keys); i++ {
		strkeys[i] = keys[i].String()
	}

	return strkeys
}
