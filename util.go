package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/smtp"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ahmdrz/goinsta/v2"
	"github.com/spf13/viper"

	tgbotapi "gopkg.in/telegram-bot-api.v4"
)

var (
	// Whether we are in development mode or not
	dev bool

	// Whether we want an email to be sent when the script ends / crashes
	nomail bool

	// Whether we want to launch the unfollow mode
	unfollow bool

	// Acut
	run bool

	// Whether we want to have logging
	logs bool

	// Used to skip following, liking and commenting same user in this session
	noduplicate bool
	// An image will be liked if the poster has more followers than likeLowerLimit, and less than likeUpperLimit
	likeLowerLimit int

	likeUpperLimit int

	// Needs to be a subset of the like interval
	followLowerLimit int
	// A user will be followed if he has more followers than followLowerLimit, and less than followUpperLimit
	followUpperLimit int

	// Needs to be a subset of the like interval
	commentLowerLimit int
	// An image will be commented if the poster has more followers than commentLowerLimit, and less than commentUpperLimit
	commentUpperLimit int
	commentCount      int

	maxLikesToAccountPerSession int

	// Hashtags list. Do not put the '#' in the config file
	tagsList map[string]interface{}

	// Limits for the current hashtag
	limits map[string]int

	// Comments list
	commentsList []string

	// Report that will be sent at the end of the script
	report map[line]int
	// report = make(map[string]map[string]int)

	userBlacklist []string
	userWhitelist []string

	// Counters that will be incremented while we like, comment, and follow
	numFollowed  int
	numLiked     int
	numCommented int

	// Will hold the tag value
	tag string

	l sync.RWMutex
)

type (
	Report struct {
		Tag, Action string
	}
	// Line is a struct to store one line of the report
	line struct {
		Tag, Action string
	}
)

func getKeys(slice interface{}) []string {
	keys := reflect.ValueOf(slice).MapKeys()
	// log.Println(keys)
	strkeys := make([]string, len(keys))
	for i := 0; i < len(keys); i++ {
		strkeys[i] = keys[i].String()
	}

	return strkeys
}

func getInput(text string) string {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(text)
	input, err := reader.ReadString('\n')
	check(err)
	return strings.TrimSpace(input)
}

// Checks if the user is in the slice
func containsUser(slice []goinsta.User, user goinsta.User) bool {
	for _, currentUser := range slice {
		if currentUser.Username == user.Username {
			return true
		}
	}
	return false
}

func getInputf(format string, args ...interface{}) string {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(format, args...)
	input, err := reader.ReadString('\n')
	check(err)
	return strings.TrimSpace(input)
}

// Same, with strings
func containsString(slice []string, user string) bool {
	for _, currentUser := range slice {
		if currentUser == user {
			return true
		}
	}
	return false
}

// check will log.Fatal if err is an error
func check(err error) {
	if err != nil {
		log.Fatal("ERROR:", err)
	}
}

// Parses the options given to the script
func parseOptions() {
	flag.BoolVar(&run, "run", false, "Use this option to follow, like and comment")
	flag.BoolVar(&unfollow, "sync", false, "Use this option to unfollow those who are not following back")
	flag.BoolVar(&nomail, "nomail", false, "Use this option to disable the email notifications")
	flag.BoolVar(&dev, "dev", false, "Use this option to use the script in development mode : nothing will be done for real")
	flag.BoolVar(&logs, "logs", false, "Use this option to enable the logfile")
	flag.BoolVar(&noduplicate, "noduplicate", false, "Use this option to skip following, liking and commenting same user in this session")

	flag.Parse()

	// -logs enables the log file
	if logs {
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
	if dev {
		folder = "local"
	}
	viper.SetConfigFile(folder + "/config.json")

	// Reads the config file
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}

	// Check enviroment
	viper.SetEnvPrefix("instabot")
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)
	viper.AutomaticEnv()

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

	reportID = viper.GetInt64("user.telegram.reportID")
	admins = viper.GetStringSlice("user.telegram.admins")
	telegramToken = viper.GetString("user.telegram.token")
	telegramProxy = viper.GetString("user.telegram.proxy")
	telegramProxyPort = viper.GetInt32("user.telegram.proxy_port")
	telegramProxyUser = viper.GetString("user.telegram.proxy_user")
	telegramProxyPassword = viper.GetString("user.telegram.proxy_password")

	userBlacklist = viper.GetStringSlice("blacklist")
	userWhitelist = viper.GetStringSlice("whitelist")

	instaUsername = viper.GetString("user.instagram.username")
	instaPassword = viper.GetString("user.instagram.password")
	instaProxy = viper.GetString("user.instagram.proxy")

	report = make(map[line]int)
}

// Sends an email. Check out the "mail" section of the "config.json" file.
func send(body string, success bool) {
	if !nomail {
		from := viper.GetString("user.mail.from")
		pass := viper.GetString("user.mail.password")
		to := viper.GetString("user.mail.to")

		status := func() string {
			if success {
				return "Success!"
			}
			return "Failure!"
		}()
		msg := "From: " + from + "\n" +
			"To: " + to + "\n" +
			"Subject:" + status + "  go-instabot\n\n" +
			body

		err := smtp.SendMail(viper.GetString("user.mail.smtp"),
			smtp.PlainAuth("", from, pass, viper.GetString("user.mail.server")),
			from, []string{to}, []byte(msg))

		if err != nil {
			log.Printf("smtp error: %s", err)
			return
		}

		log.Print("sent")
	}

	// send to telegram
	bot, err := tgbotapi.NewBotAPI(viper.GetString("user.telegram.token"))
	if err != nil {
		log.Println(err)
		return
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
	return fmt.Errorf("after %d attempts, last error: %s", maxAttempts, err)
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

// Builds the report prints it and sends it
func buildReport() {
	reportAsString := ""
	for index, element := range report {
		var times string
		if element == 1 {
			times = "time"
		} else {
			times = "times"
		}
		if index.Action == "like" {
			reportAsString += fmt.Sprintf("#%s has been liked %d %s\n", index.Tag, element, times)
		} else {
			reportAsString += fmt.Sprintf("#%s has been %sed %d %s\n", index.Tag, index.Action, element, times)
		}
	}

	// Displays the report on the screen / log file
	fmt.Println(reportAsString)

	// Sends the report to the email in the config file, if the option is enabled
	send(reportAsString, true)
}

func stringInStringSlice(a string, list []string) bool {
	for _, b := range list {
		if a == b {
			return true
		}
	}
	return false
}

func sliceUnique(input []string) []string {
	u := make([]string, 0, len(input))
	m := make(map[string]bool)

	for _, val := range input {
		if _, ok := m[val]; !ok {
			m[val] = true
			u = append(u, val)
		}
	}

	return u
}

// Checks if the user is in the slice
func contains(slice []goinsta.User, user goinsta.User) bool {
	for index := range slice {
		if user.ID == slice[index].ID {
			return true
		}
	}
	return false
}

func intInStringSlice(a int, list []string) bool {
	b := strconv.Itoa(a)
	for _, c := range list {
		if b == c {
			return true
		}
	}
	return false
}

func shuffle(slice interface{}) {
	rv := reflect.ValueOf(slice)
	swap := reflect.Swapper(slice)
	length := rv.Len()
	for i := length - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		swap(i, j)
	}
}

// ControlManager main func
func ControlManager(name string, fn func(name string) error, autostart bool) (startChan, stopChan chan bool) {
	startChan = make(chan bool)
	stopChan = make(chan bool)

	go func() {
		for {
			select {
			case <-startChan:
				l.Lock()
				if !isStarted[name] {
					isStarted[name] = true
					l.Unlock()
					go func() {
						fn(name)
						stopChan <- true
					}()
				} else {
					l.Unlock()
				}
			case <-stopChan:
				l.Lock()
				if isStarted[name] {
					isStarted[name] = false
					l.Unlock()
				} else {
					l.Unlock()
				}
			}

		}
	}()

	if autostart {
		startChan <- true
	}

	return startChan, stopChan
}
