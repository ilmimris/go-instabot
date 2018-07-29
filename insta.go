package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ahmdrz/goinsta"
	"github.com/ahmdrz/goinsta/response"
	"github.com/boltdb/bolt"
	"github.com/spf13/viper"
	"github.com/tducasse/goinsta/store"
	"gopkg.in/telegram-bot-api.v4"
)

// Insta is a goinsta.Instagram instance
var insta *goinsta.Instagram

var usersInfo = make(map[string]response.GetUsernameResponse)
var tagFeed = make(map[string]response.TagFeedsResponse)

// login will try to reload a previous session, and will create a new one if it can't
func login() {
	err := reloadSession()
	if err != nil {
		createAndSaveSession()
	}
}

func refollowManager(db *bolt.DB) (startChan chan bool, outerChan, innerChan chan string, stopChan chan bool) {
	startChan = make(chan bool)
	outerChan = make(chan string)
	innerChan = make(chan string)
	stopChan = make(chan bool)
	go func() {
		for {
			select {
			case <-startChan:
				if !refollowIsStarted.IsSet() {
					refollowIsStarted.Set()
					go followFollowers(db, innerChan, stopChan)
					// innerChan <- "start"
				} else {
					fmt.Println("can't start task, task already running!")
				}
			case msg := <-outerChan:
				fmt.Println("refollow <- ", msg)
				//default:
				//	time.Sleep(100 * time.Millisecond)
			}
		}
	}()
	return startChan, outerChan, innerChan, stopChan
}

func followFollowers(db *bolt.DB, innerChan chan string, stopChan chan bool) {
	defer refollowIsStarted.UnSet()
	for {
		select {
		case msg := <-innerChan:
			fmt.Println("refollow <- ", msg)
			go func() {
				state["refollow"] = 0
				time.Sleep(1 * time.Second)
				username := msg
				user, err := insta.GetUserByUsername(username)
				if err != nil {
					followFollowersRes <- TelegramResponse{fmt.Sprintf("%s", err)}
					stopChan <- true
					return
				} else {
					if user.User.IsPrivate {
						userFriendShip, err := insta.UserFriendShip(user.User.ID)
						check(err)
						if !userFriendShip.Following {
							followFollowersRes <- TelegramResponse{"User profile is private and we are not following, can't process"}
							stopChan <- true
							return
						}
					}

					followers, err := insta.TotalUserFollowers(user.User.ID)
					//log.Println(followers)
					check(err)
					var users = followers.Users
					if len(users) > 0 {
						rand.Seed(time.Now().UnixNano()) // do it once during app initialization
						Shuffle(users)
					}

					var limit = viper.GetInt("limits.maxSync")
					if limit <= 0 || limit >= 1000 {
						limit = 1000
					}

					today, _ := getStats(db, "refollow")
					if today > 0 {
						limit = limit - today
					}

					var allCount = int(math.Min(float64(len(users)), float64(limit)))
					switch {
					case allCount == 0 && len(users) > 0:
						followFollowersRes <- TelegramResponse{"Follow limit reached :("}
					case allCount <= 0:
						followFollowersRes <- TelegramResponse{"Followers not found :("}
					default:
						var current = 0

						followFollowersRes <- TelegramResponse{fmt.Sprintf("%d users will be followed", allCount)}

						for index := range users {
							if !refollowIsStarted.IsSet() {
								stopChan <- true
								return
							}
							if current >= limit {
								continue
							}

							if users[index].IsPrivate {
								log.Printf("%s is private, skipping\n", users[index].Username)
							} else {
								previoslyFollowed, _ := getFollowed(db, users[index].Username)
								if previoslyFollowed != "" {
									log.Printf("%s previously followed at %s, skipping\n", users[index].Username, previoslyFollowed)
								} else {
									current++
									state["refollow"] = int(current * 100 / allCount)
									state["refollow_current"] = current
									state["refollow_all_count"] = allCount

									text := fmt.Sprintf("[%d/%d] refollowing %s (%d%%)", state["refollow_current"], state["refollow_all_count"], users[index].Username, state["refollow"])
									followFollowersRes <- TelegramResponse{text}

									if !*dev {
										insta.Follow(users[index].ID)
										setFollowed(db, users[index].Username)
										incStats(db, "follow")
										incStats(db, "refollow")
										time.Sleep(10 * time.Second)
									} else {
										time.Sleep(2 * time.Second)
									}
								}
							}
						}
					}
				}
				stopChan <- true
			}()
		case <-stopChan:
			editMessage["refollow"] = make(map[int]int)
			state["refollow"] = -1
			followFollowersRes <- TelegramResponse{fmt.Sprintf("\nRefollowed %d users!\n", state["refollow_current"])}
			return
			//default:
			//	time.Sleep(100 * time.Millisecond)
		}
	}
}

func unfollowManager(db *bolt.DB) (startChan chan bool, outerChan, innerChan chan string, stopChan chan bool) {
	startChan = make(chan bool)
	outerChan = make(chan string)
	innerChan = make(chan string)
	stopChan = make(chan bool)
	go func() {
		for {
			select {
			case <-startChan:
				if !unfollowIsStarted.IsSet() {
					unfollowIsStarted.Set()
					go syncFollowers(db, innerChan, stopChan)
					innerChan <- "start"
				} else {
					fmt.Println("can't start task, task already running!")
				}
			case msg := <-outerChan:
				fmt.Println("unfollow <- ", msg)
				//default:
				//	time.Sleep(100 * time.Millisecond)
			}
		}
	}()
	return startChan, outerChan, innerChan, stopChan
}

func syncFollowers(db *bolt.DB, innerChan chan string, stopChan chan bool) {
	defer unfollowIsStarted.UnSet()
	for {
		select {
		case msg := <-innerChan:
			fmt.Println("unfollow <- ", msg)
			go func() {
				state["unfollow"] = 0
				time.Sleep(1 * time.Second)
				following, _ := insta.SelfTotalUserFollowing()
				// check(err)
				followers, _ := insta.SelfTotalUserFollowers()
				// check(err)

				var daysBeforeUnfollow = viper.GetInt("limits.daysBeforeUnfollow")
				if daysBeforeUnfollow <= 0 || daysBeforeUnfollow >= 30 {
					daysBeforeUnfollow = 3
				}

				var users []response.User
				for index := range following.Users {
					if !contains(followers.Users, following.Users[index]) {
						previoslyFollowed, _ := getFollowed(db, following.Users[index].Username)
						if previoslyFollowed != "" {
							t, err := time.Parse("20060102", previoslyFollowed)

							if err != nil {
								fmt.Println(err)
							} else {
								duration := time.Since(t)
								if int(duration.Hours()) < (24 * daysBeforeUnfollow) {
									fmt.Printf("%s not followed us less then %f hours, skipping!\n", following.Users[index].Username, duration.Hours())
									continue
								} else {
									users = append(users, following.Users[index])
								}
							}
						} else {
							users = append(users, following.Users[index])
						}
					}
				}

				lastLikers := getLastLikers()
				if len(lastLikers) > 0 {
					if len(following.Users) > 0 {
						unfollowRes <- TelegramResponse{fmt.Sprintf("Found %d following, %d likers for last 10 posts\n", len(following.Users), len(lastLikers))}
						var notLikers []response.User
						for index := range following.Users {
							if !stringInStringSlice(following.Users[index].Username, lastLikers) {
								notLikers = append(notLikers, following.Users[index])
							}
						}

						if len(notLikers) > 0 {
							for index := range notLikers {
								previoslyFollowed, _ := getFollowed(db, notLikers[index].Username)
								if previoslyFollowed != "" {
									t, err := time.Parse("20060102", previoslyFollowed)

									if err != nil {
										fmt.Println(err)
									} else {
										duration := time.Since(t)
										if int(duration.Hours()) < (24 * daysBeforeUnfollow) {

										} else {
											if !contains(users, notLikers[index]) {
												users = append(users, notLikers[index])
											}
										}
									}
								} else {
									if !contains(users, notLikers[index]) {
										users = append(users, notLikers[index])
									}
								}
							}
						}
					}
				}

				var limit = viper.GetInt("limits.maxSync")
				if limit <= 0 || limit >= 1000 {
					limit = 1000
				}

				today, _ := getStats(db, "unfollow")
				if today > 0 {
					limit = limit - today
				}

				var current = 0
				var allCount = int(math.Min(float64(len(users)), float64(limit)))
				if allCount > 0 {
					unfollowRes <- TelegramResponse{fmt.Sprintf("%d will be unfollowed", allCount)}

					for index := range users {
						if !unfollowIsStarted.IsSet() {
							stopChan <- true
							return
						}

						if current >= limit {
							continue
						}

						current++
						state["unfollow"] = int(current * 100 / allCount)
						state["unfollow_current"] = current
						state["unfollow_all_count"] = allCount

						unfollowRes <- TelegramResponse{fmt.Sprintf("[%d/%d] Unfollowing %s (%d%%)\n", state["unfollow_current"], state["unfollow_all_count"], users[index].Username, state["unfollow"])}
						if !*dev {
							insta.UnFollow(users[index].ID)
							setFollowed(db, users[index].Username)
							incStats(db, "unfollow")
							time.Sleep(60 * time.Second)
						} else {
							time.Sleep(2 * time.Second)
						}
					}
				}

				stopChan <- true
			}()
		case <-stopChan:
			editMessage["unfollow"] = make(map[int]int)
			state["unfollow"] = -1

			if state["unfollow_current"] == 0 {
				unfollowRes <- TelegramResponse{fmt.Sprintf("No one unfollow")}
			} else {
				unfollowRes <- TelegramResponse{fmt.Sprintf("\nUnfollowed %d users are not following you back!\n", state["unfollow_current"])}
				state["unfollow_current"] = 0
				state["unfollow_all_count"] = 0
			}
			return
			//default:
			//	time.Sleep(100 * time.Millisecond)
		}
	}
}

// Logins and saves the session
func createAndSaveSession() {
	insta = goinsta.New(instaUsername, instaPassword)
	err := insta.Login()
	check(err)

	key := createKey()
	bytes, err := store.Export(insta, key)
	check(err)
	err = ioutil.WriteFile("session", bytes, 0644)
	check(err)
	log.Println("Created and saved the session")
}

// reloadSession will attempt to recover a previous session
func reloadSession() error {
	if _, err := os.Stat("session"); os.IsNotExist(err) {
		return errors.New("No session found")
	}

	session, err := ioutil.ReadFile("session")
	check(err)
	log.Println("A session file exists")

	key, err := ioutil.ReadFile("key")
	check(err)

	insta, err = store.Import(session, key)
	if err != nil {
		return errors.New("Couldn't recover the session")
	}

	log.Println("Successfully logged in")
	return nil

}

// createKey creates a key and saves it to file
func createKey() []byte {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	check(err)
	err = ioutil.WriteFile("key", key, 0644)
	check(err)
	log.Println("Created and saved the key")
	return key
}

func followManager(db *bolt.DB) (startChan chan bool, outerChan, innerChan chan string, stopChan chan bool) {
	startChan = make(chan bool)
	outerChan = make(chan string)
	innerChan = make(chan string)
	stopChan = make(chan bool)
	go func() {
		for {
			select {
			case <-startChan:
				if !followIsStarted.IsSet() {
					followIsStarted.Set()
					go loopTags(db, innerChan, stopChan)
					innerChan <- "start"
				} else {
					fmt.Println("can't start task, task already running!")
				}
			case msg := <-outerChan:
				fmt.Println("follow <- ", msg)
				//default:
				//	time.Sleep(100 * time.Millisecond)
			}
		}
	}()
	return startChan, outerChan, innerChan, stopChan
}

// Go through all the tags in the list
func loopTags(db *bolt.DB, innerChan chan string, stopChan chan bool) {
	usersInfo = make(map[string]response.GetUsernameResponse)
	tagFeed = make(map[string]response.TagFeedsResponse)

	defer followIsStarted.UnSet()
	for {
		select {
		case msg := <-innerChan:
			fmt.Println("follow <- ", msg)
			go func() {
				state["follow"] = 0
				time.Sleep(1 * time.Second)

				report = make(map[string]map[string]int)
				likesToAccountPerSession = make(map[string]int)

				time.Sleep(1 * time.Second)
				var allCount = len(tagsList)
				if allCount > 0 {
					var current = 0

					Shuffle(tagsList)
					for _, tag := range tagsList {
						if !followIsStarted.IsSet() {
							stopChan <- true
							return
						}

						current++

						state["follow"] = int(current * 100 / allCount)
						state["follow_current"] = current
						state["follow_all_count"] = allCount

						// limitsConf := viper.GetStringMap("tags." + tag)

						// // Some converting
						// limits = map[string]int{
						// 	"follow":  int(limitsConf["follow"].(float64)),
						// 	"like":    int(limitsConf["like"].(float64)),
						// 	"comment": int(limitsConf["comment"].(float64)),
						// }

						// What we did so far
						numFollowed = 0
						numLiked = 0
						numCommented = 0

						text := fmt.Sprintf("\n[%d/%d] ➜ Current tag is %s (%d%%)\n", state["follow_current"], state["follow_all_count"], tag, state["follow"])
						followRes <- TelegramResponse{text}
						browse(tag, db, stopChan)
						time.Sleep(10 * time.Second)
					}
				}

				stopChan <- true
			}()
		case <-stopChan:
			followRes <- TelegramResponse{"Follow finished"}

			editMessage["follow"] = make(map[int]int)
			state["follow"] = -1

			reportAsString := ""
			for tag, _ := range report {
				reportAsString += fmt.Sprintf("#%s: %d 🐾, %d 👍, %d 💌\n", tag, report[tag]["follow"], report[tag]["like"], report[tag]["comment"])
			}
			if reportAsString != "" {
				followRes <- TelegramResponse{reportAsString}
			}
			return
			//default:
			//	time.Sleep(100 * time.Millisecond)
		}
	}
}

// Browses the page for a certain tag, until we reach the limits
func browse(tag string, db *bolt.DB, stopChan chan bool) {
	var i = 0
	for numFollowed < followCount || numLiked < likeCount || numCommented < commentCount {
		if !followIsStarted.IsSet() {
			stopChan <- true
			return
		}

		log.Println("Fetching the list of images for #" + tag)
		i++

		// Getting all the pictures we can on the first page
		// Instagram will return a 500 sometimes, so we will retry 10 times.
		// Check retry() for more info.

		var images, ok = tagFeed[tag]
		if ok {
			// log.Println("from cache #" + tag)
		} else {
			err := retry(10, 20*time.Second, func() (err error) {
				images, err = insta.TagFeed(tag)
				if err == nil {
					tagFeed[tag] = images
				}
				return
			})
			check(err)
		}

		goThrough(tag, db, images, stopChan)

		if viper.IsSet("limits.maxRetry") && i > viper.GetInt("limits.maxRetry") {
			log.Println("Currently not enough images for this tag to achieve goals")
			break
		}
	}
}

// Goes through all the images for a certain tag
func goThrough(tag string, db *bolt.DB, images response.TagFeedsResponse, stopChan chan bool) {
	var i = 1
	for index := range images.FeedsResponse.Items {
		if !followIsStarted.IsSet() {
			stopChan <- true
			return
		}
		// Exiting the loop if there is nothing left to do
		if numFollowed >= followCount && numLiked >= likeCount && numCommented >= commentCount {
			break
		}

		// Skip our own images
		if images.FeedsResponse.Items[index].User.Username == instaUsername {
			continue
		}

		// Check if we should fetch new images for tag
		if i >= followCount && i >= likeCount && i >= commentCount {
			break
		}

		// Getting the user info
		// Instagram will return a 500 sometimes, so we will retry 10 times.
		// Check retry() for more info.
		var posterInfo, ok = usersInfo[images.FeedsResponse.Items[index].User.Username]
		if ok {
			// log.Println("from cache " + posterInfo.User.Username + " - for #" + tag)
		} else {
			err := retry(10, 20*time.Second, func() (err error) {
				posterInfo, err = insta.GetUserByID(images.FeedsResponse.Items[index].User.ID)
				if err == nil {
					usersInfo[images.FeedsResponse.Items[index].User.Username] = posterInfo
				}
				return
			})
			check(err)
		}

		poster := posterInfo.User
		followerCount := poster.FollowerCount
		likesCount := images.FeedsResponse.Items[index].LikeCount
		commentsCount := images.FeedsResponse.Items[index].CommentCount

		// Will only follow and comment if we like the picture
		like := numLiked < likeCount && !images.FeedsResponse.Items[index].HasLiked
		follow := numFollowed < followCount && like
		comment := numCommented < commentCount && like

		// log.Println("Checking followers for " + poster.Username + " - for #" + tag)
		if followerCount > followUpperLimit {
			log.Printf("%s has %d followers, more than max %d\n", poster.Username, followerCount, followUpperLimit)
			follow = false
		} else if followerCount < followLowerLimit {
			log.Printf("%s has %d followers, less than min %d\n", poster.Username, followerCount, followLowerLimit)
			follow = false
		}

		if likesCount > likeUpperLimit {
			log.Printf("%s's image has %d likes, more than max %d\n", poster.Username, likesCount, likeUpperLimit)
			like = false
		} else if likesCount < likeLowerLimit {
			log.Printf("%s's image has %d likes, less than min %d\n", poster.Username, likesCount, likeLowerLimit)
			like = false
		}

		if commentsCount > commentUpperLimit {
			log.Printf("%s's image has %d comments, more than max %d\n", poster.Username, commentsCount, commentUpperLimit)
			comment = false
		} else if commentsCount < commentLowerLimit {
			log.Printf("%s's image has %d comments, less than min %d\n", poster.Username, commentsCount, commentLowerLimit)
			comment = false
		}

		if like || comment || follow {
			log.Printf("%s has %d followers\n", poster.Username, followerCount)

			i++
			// Like, then comment/follow
			if like {
				if userLikesCount, ok := likesToAccountPerSession[posterInfo.User.Username]; ok {
					if userLikesCount < maxLikesToAccountPerSession {
						likeImage(tag, db, images.FeedsResponse.Items[index], posterInfo)
						images.FeedsResponse.Items[index].HasLiked = true
					} else {
						log.Println("Likes count per user reached [" + poster.Username + "]")
					}
				} else {
					likeImage(tag, db, images.FeedsResponse.Items[index], posterInfo)
				}

				previoslyFollowed, _ := getFollowed(db, posterInfo.User.Username)
				if previoslyFollowed != "" {
					log.Printf("%s already following (%s), skipping\n", posterInfo.User.Username, previoslyFollowed)
				} else {
					if comment {
						if !images.FeedsResponse.Items[index].HasLiked {
							commentImage(tag, db, images.FeedsResponse.Items[index])
						}
					}
					if follow {
						followUser(tag, db, posterInfo)
					}
				}

				// This is to avoid the temporary ban by Instagram
				time.Sleep(30 * time.Second)
			}
		} else {
			log.Printf("%s, nothing to do\n", poster.Username)
		}
		// log.Printf("%s done\n\n", poster.Username)
	}
}

// Likes an image, if not liked already
func likeImage(tag string, db *bolt.DB, image response.MediaItemResponse, userInfo response.GetUsernameResponse) {
	log.Println("Liking the picture https://www.instagram.com/p/" + image.Code)
	if !image.HasLiked {
		if !*dev {
			insta.Like(image.ID)
		}
		// log.Println("Liked")
		numLiked++

		if _, ok := report[tag]; !ok {
			report[tag] = make(map[string]int)
		}
		report[tag]["like"]++
		incStats(db, "like")
		likesToAccountPerSession[userInfo.User.Username]++
	} else {
		// log.Println("Image already liked")
	}
}

// Comments an image
func commentImage(tag string, db *bolt.DB, image response.MediaItemResponse) {
	rand.Seed(time.Now().Unix())
	text := commentsList[rand.Intn(len(commentsList))]
	if !*dev {
		insta.Comment(image.ID, text)
	}
	log.Println("Commented " + text)
	numCommented++

	if _, ok := report[tag]; !ok {
		report[tag] = make(map[string]int)
	}
	report[tag]["comment"]++
	incStats(db, "comment")
}

// Follows a user, if not following already
func followUser(tag string, db *bolt.DB, userInfo response.GetUsernameResponse) {
	user := userInfo.User

	userFriendShip, err := insta.UserFriendShip(user.ID)
	check(err)
	// If not following already
	if !userFriendShip.Following {

		if !*dev {
			if user.IsPrivate {
				log.Printf("%s is private, skipping follow\n", user.Username)
			} else {
				log.Printf("Following %s\n", user.Username)
				resp, err := insta.Follow(user.ID)
				if err != nil {
					log.Println(err)
				} else {
					userFriendShip.Following = resp.FriendShipStatus.Following

					if !userFriendShip.Following {
						log.Println("Not followed")
					}
				}
			}
		}
		// log.Println("Followed")
		if userFriendShip.Following {
			numFollowed++
			if _, ok := report[tag]; !ok {
				report[tag] = make(map[string]int)
			}
			report[tag]["follow"]++
			incStats(db, "follow")
			setFollowed(db, user.Username)
		}
	} else {
		log.Println("Already following " + user.Username)
	}
}

func startFollow(bot *tgbotapi.BotAPI, startChan chan bool, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")
	if followIsStarted.IsSet() {
		msg.Text = fmt.Sprintf("Follow in progress (%d%%)", state["follow"])
		if len(editMessage["follow"]) > 0 && intInStringSlice(int(userID), GetKeys(editMessage["follow"])) {
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
				editMessage["follow"][int(userID)] = msgRes.MessageID
			}
		}
	} else {
		startChan <- true
		msg.Text = "Starting follow"
		msgRes, err := bot.Send(msg)
		if err == nil {
			editMessage["follow"][int(userID)] = msgRes.MessageID
		}
	}
}

func startUnfollow(bot *tgbotapi.BotAPI, startChan chan bool, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")
	if unfollowIsStarted.IsSet() {
		msg.Text = fmt.Sprintf("Unfollow in progress (%d%%)", state["unfollow"])
		if len(editMessage["unfollow"]) > 0 && intInStringSlice(int(userID), GetKeys(editMessage["unfollow"])) {
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
				editMessage["unfollow"][int(userID)] = msgRes.MessageID
			}
		}
	} else {
		startChan <- true
		msg.Text = "Starting unfollow"
		fmt.Println(msg.Text)
		msgRes, err := bot.Send(msg)
		if err == nil {
			editMessage["unfollow"][int(userID)] = msgRes.MessageID
		}
	}
}

func startRefollow(bot *tgbotapi.BotAPI, startChan chan bool, innerRefollowChan chan string, userID int64, target string) {
	msg := tgbotapi.NewMessage(userID, "")
	if refollowIsStarted.IsSet() {
		msg.Text = fmt.Sprintf("Refollow in progress (%d%%)", state["refollow"])
		if len(editMessage["refollow"]) > 0 && intInStringSlice(int(userID), GetKeys(editMessage["refollow"])) {
			edit := tgbotapi.EditMessageTextConfig{
				BaseEdit: tgbotapi.BaseEdit{
					ChatID:    int64(userID),
					MessageID: editMessage["refollow"][int(userID)],
				},
				Text: msg.Text,
			}
			bot.Send(edit)
		} else {
			msgRes, err := bot.Send(msg)
			if err == nil {
				editMessage["refollow"][int(userID)] = msgRes.MessageID
			}
		}
	} else {
		startChan <- true
		msg.Text = "Starting refollow"
		msgRes, err := bot.Send(msg)
		if err == nil {
			editMessage["refollow"][int(userID)] = msgRes.MessageID
		}
		innerRefollowChan <- target
	}
}

func sendStats(bot *tgbotapi.BotAPI, db *bolt.DB, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")
	unfollowCount, _ := getStats(db, "unfollow")
	followCount, _ := getStats(db, "follow")
	refollowCount, _ := getStats(db, "refollow")
	likeCount, _ := getStats(db, "like")
	commentCount, _ := getStats(db, "comment")
	if unfollowCount > 0 || followCount > 0 || refollowCount > 0 || likeCount > 0 || commentCount > 0 {
		msg.Text = fmt.Sprintf("Unfollowed: %d\nFollowed: %d\nRefollowed: %d\nLiked: %d\nCommented: %d", unfollowCount, followCount, refollowCount, likeCount, commentCount)
		if userID == -1 {
			for _, id := range admins {
				userID, _ = strconv.ParseInt(id, 10, 64)
				msg.ChatID = userID
				bot.Send(msg)
			}
		} else {
			bot.Send(msg)
		}
	}
}

func sendComments(bot *tgbotapi.BotAPI, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")
	if len(commentsList) > 0 {
		msg.Text = strings.Join(commentsList, ", ")
	} else {
		msg.Text = "Comments is empty"
	}

	bot.Send(msg)
}

func addComments(bot *tgbotapi.BotAPI, comments string, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")
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

func removeComments(bot *tgbotapi.BotAPI, comments string, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")
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

func sendTags(bot *tgbotapi.BotAPI, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")
	if len(tagsList) > 0 {
		// keys := GetKeys(tagsList)
		// msg.Text = strings.Join(keys, ", ")
		msg.Text = strings.Join(tagsList, ", ")
	} else {
		msg.Text = "Tags is empty"
	}

	bot.Send(msg)
}

func addTags(bot *tgbotapi.BotAPI, tag string, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")
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

func removeTags(bot *tgbotapi.BotAPI, tags string, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")
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

func getLimits(bot *tgbotapi.BotAPI, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")

	limits := []string{"maxSync", "daysBeforeUnfollow", "max_likes_to_account_per_session", "maxRetry", "like.min", "like.count", "like.max", "follow.min", "follow.count", "follow.max", "comment.min", "comment.count", "comment.max"}
	for _, limit := range limits {
		msg.Text += limit + ": " + strconv.Itoa(viper.GetInt("limits."+limit)) + "\n"
	}

	bot.Send(msg)
}

func updateLimits(bot *tgbotapi.BotAPI, limitStr string, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")
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

func getLastLikers() (result []string) {
	latest, _ := insta.LatestFeed()
	l := latest.Items[0:10] //last 10 posts
	for lindex := range l {
		// log.Println(item.ID, item.HasLiked, item.LikeCount)
		if l[lindex].LikeCount > 0 {
			likers, _ := insta.MediaLikers(l[lindex].ID)
			for index := range likers.Users {
				// log.Println(liker.Username, liker.HasAnonymousProfilePicture)
				if !stringInStringSlice(likers.Users[index].Username, result) {
					result = append(result, likers.Users[index].Username)
				}
			}
		}
	}
	// log.Println(len(result), result)
	return result
}

func likeFollowersPosts(db *bolt.DB) {
	timeline, _ := insta.Timeline("")
	length := len(timeline.Items)
	if length > 20 {
		length = 20
	}

	var usernames []string

	if length > 0 {
		items := timeline.Items[0:length]
		for index := range items {
			// log.Println(item.ID, item.Caption, item.User.Username)
			if !items[index].HasLiked {
				time.Sleep(5 * time.Second)
				insta.Like(items[index].ID)
				incStats(db, "like")

				usernames = append(usernames, items[index].User.Username)
			}
		}
		length = len(usernames)
		if length > 0 {
			usernames = SliceUnique(usernames)
			log.Println("liked", strings.Join(usernames, ", "))
		}
	}
}
