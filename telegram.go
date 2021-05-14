package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ad/cron"
	"github.com/ahmdrz/goinsta/v2"
	"github.com/boltdb/bolt"
	"github.com/spf13/viper"
	tgbotapi "gopkg.in/telegram-bot-api.v4"
)

var (
	insta          *goinsta.Instagram
	usersInfo      = make(map[string]goinsta.User)
	tagFeed        = make(map[string]goinsta.Item)
	isStarted      = make(map[string]bool)
	reportAsString string

	// A user will be followed if he has more followers than followLowerLimit, and less than followUpperLimit
	// Needs to be a subset of the like interval
	followCount  int
	potencyRatio float64

	// An image will be liked if the poster has more followers than likeLowerLimit, and less than likeUpperLimit
	likeCount int
)

func getStatus() (result string) {
	userinfo, err := insta.Profiles.ByName(insta.Account.Username)
	if err != nil {
		log.Println("getStatus", insta.Account.Username, err)
	} else {
		result = fmt.Sprintf("🖼%d, 👀%d, 🐾%d", userinfo.MediaCount, userinfo.FollowerCount, userinfo.FollowingCount)
	}

	return
}

func refollow(name string, db *bolt.DB, innerChan chan string) error {
	for {
		select {
		case msg := <-innerChan:
			fmt.Println("refollow <- ", msg)
			l.Lock()
			state["refollow"] = 0
			l.Unlock()

			time.Sleep(1 * time.Second)
			username := msg
			user, err := insta.Profiles.ByName(username)
			if err != nil {
				telegramResp <- telegramResponse{fmt.Sprintf("%s", err), "refollow"}
				return nil
			}

			if user.IsPrivate {
				// userFriendShip, err := insta.UserFriendShip(user.User.ID)
				// check(err)
				if !user.Friendship.Following {
					telegramResp <- telegramResponse{"User profile is private and we are not following, can't process", "refollow"}
					return nil
				}
			}

			followers := user.Following()
			for followers.Next() {
				users := followers.Users

				if len(users) > 0 {
					rand.Seed(time.Now().UnixNano()) // do it once during app initialization
					shuffle(users)
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
					telegramResp <- telegramResponse{"Follow limit reached :(", "refollow"}
				case allCount <= 0:
					telegramResp <- telegramResponse{"Followers not found :(", "refollow"}
				default:
					var current = 0

					telegramResp <- telegramResponse{fmt.Sprintf("%d users will be followed", allCount), "refollow"}

					for index := range users {
						l.RLock()
						if !isStarted[name] {
							l.RUnlock()

							telegramResp <- telegramResponse{fmt.Sprintf("\nRefollowed %d users!", state["refollow_current"]), "refollow"}

							l.Lock()
							editMessage["refollow"] = make(map[int]int)
							state["refollow"] = -1
							l.Unlock()

							return nil
						}
						l.RUnlock()

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

								l.Lock()
								state["refollow"] = int(current * 100 / allCount)
								state["refollow_current"] = current
								state["refollow_all_count"] = allCount
								l.Unlock()

								text := fmt.Sprintf("[%d/%d] refollowing %s (%d%%)", state["refollow_current"], state["refollow_all_count"], users[index].Username, state["refollow"])
								telegramResp <- telegramResponse{text, "refollow"}

								if !dev {
									users[index].Follow()
									// insta.Follow(users[index].ID)
									setFollowed(db, users[index].Username)
									incStats(db, "follow")
									incStats(db, "refollow")
									time.Sleep(16 * time.Second)
								} else {
									time.Sleep(2 * time.Second)
								}
							}
						}
					}
				}
			}

			telegramResp <- telegramResponse{fmt.Sprintf("\nRefollowed %d users!", state["refollow_current"]), "refollow"}

			l.Lock()
			editMessage["refollow"] = make(map[int]int)
			state["refollow"] = -1
			l.Unlock()
		}
	}
}

func followLikers(name string, db *bolt.DB, innerChan chan string) error {
	for {
		select {
		case msg := <-innerChan:
			l.Lock()
			state["followLikers"] = 0
			l.Unlock()

			if len(msg) > 0 {
				u, err := url.Parse(msg)
				if err == nil {
					test := strings.TrimPrefix(u.Path, "/p/")
					test = strings.TrimSuffix(test, "/")

					mediaID, err := goinsta.MediaIDFromShortID(test)
					if err != nil {
						fmt.Println(err)
					} else {
						media, err := insta.GetMedia(mediaID)
						if err != nil {
							fmt.Println(err)
						} else {
							users := media.Items[0].Likers
							if len(users) > 0 {
								println(fmt.Sprintf("found %d likers", len(users)))
								rand.Seed(time.Now().UnixNano()) // do it once during app initialization
								shuffle(users)

								var limit = viper.GetInt("limits.maxSync")
								if limit <= 0 || limit >= 1000 {
									limit = 1000
								}

								today, _ := getStats(db, "followLikers")
								if today > 0 {
									limit = limit - today
								}

								var allCount = int(math.Min(float64(len(users)), float64(limit)))
								switch {
								case allCount == 0 && len(users) > 0:
									telegramResp <- telegramResponse{"Follow limit reached :(", "followLikers"}
								case allCount <= 0:
									telegramResp <- telegramResponse{"Likers not found :(", "followLikers"}
								default:
									var current = 0

									telegramResp <- telegramResponse{fmt.Sprintf("%d users will be followed", allCount), "followLikers"}

									for index := range users {
										l.RLock()
										if !isStarted[name] {
											l.RUnlock()

											telegramResp <- telegramResponse{fmt.Sprintf("\nfollowed %d users!", state["followLikers_current"]), "followLikers"}

											l.Lock()
											editMessage["followLikers"] = make(map[int]int)
											state["followLikers"] = -1
											l.Unlock()

											return nil
										}
										l.RUnlock()

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

												l.Lock()
												state["followLikers"] = int(current * 100 / allCount)
												state["followLikers_current"] = current
												state["followLikers_all_count"] = allCount
												l.Unlock()

												text := fmt.Sprintf("[%d/%d] following %s (%d%%)", state["followLikers_current"], state["followLikers_all_count"], users[index].Username, state["followLikers"])
												telegramResp <- telegramResponse{text, "followLikers"}

												if !dev {
													users[index].Follow()
													// insta.Follow(users[index].ID)
													setFollowed(db, users[index].Username)
													incStats(db, "follow")
													incStats(db, "followLikers")
													time.Sleep(16 * time.Second)
												} else {
													time.Sleep(2 * time.Second)
												}
											}
										}
									}
									telegramResp <- telegramResponse{fmt.Sprintf("\nfollowed %d users!", state["followLikers_current"]), "followLikers"}

									l.Lock()
									editMessage["followLikers"] = make(map[int]int)
									state["followLikers"] = -1
									l.Unlock()
								}
							} else {
								println("likers not found")
							}
						}
					}
				}
			}
		}
	}
	// return nil
}

func sendStats(bot *tgbotapi.BotAPI, db *bolt.DB, c *cron.Cron, userID int64) {
	message := `<i>%s</i>
<b>Today stats</b>
Unfollowed — %d
Followed — %d
Refollowed — %d
Followed likers — %d
Liked — %d
Commented — %d`

	unfollowCount, _ := getStats(db, "unfollow")
	followCount, _ := getStats(db, "follow")
	refollowCount, _ := getStats(db, "refollow")
	followLikersCount, _ := getStats(db, "followLikers")
	likeCount, _ := getStats(db, "like")
	commentCount, _ := getStats(db, "comment")

	stats := getStatus()

	msg := tgbotapi.NewMessage(userID, fmt.Sprintf(message,
		stats,
		unfollowCount,
		followCount,
		refollowCount,
		followLikersCount,
		likeCount,
		commentCount,
		// getJobState(c, cronFollow),
		// getJobState(c, cronUnfollow),
		// getJobState(c, cronStats),
		// getJobState(c, cronLike),
	))

	msg.DisableWebPagePreview = true
	msg.ParseMode = "HTML"
	msg.DisableNotification = true

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
		newComments = sliceUnique(newComments)
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
		newComments = sliceUnique(newComments)
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
		newTags = sliceUnique(newTags)
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
		newTags = sliceUnique(newTags)
		viper.Set("tags", newTags)
		viper.WriteConfig()
		msg.Text = "Tags removed"
	} else {
		msg.Text = "Tags is empty"
	}

	bot.Send(msg)
}

func sendWhitelist(bot *tgbotapi.BotAPI, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(userWhitelist) > 0 {
		// keys := GetKeys(tagsList)
		// msg.Text = strings.Join(keys, ", ")
		msg.Text = strings.Join(userWhitelist, ", ")
	} else {
		msg.Text = "whitelist is empty"
	}

	bot.Send(msg)
}

func addWhitelist(bot *tgbotapi.BotAPI, item string, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	// if len(tags) > 0 {
	item = strings.Replace(item, ".", "", -1)
	if len(item) > 0 {
		newWhiteList := strings.Split(item, ", ")
		newWhiteList = append(userWhitelist, newWhiteList...)
		newWhiteList = sliceUnique(newWhiteList)
		viper.Set("whitelist", newWhiteList)
		viper.WriteConfig()
		msg.Text = "whiteList added"
	} else {
		msg.Text = "Item is empty"
	}

	bot.Send(msg)
}

func removeWhitelist(bot *tgbotapi.BotAPI, items string, UserID int64) {
	msg := tgbotapi.NewMessage(UserID, "")
	if len(items) > 0 {
		removeWhiteList := strings.Split(items, ", ")
		var newWhiteList []string
		for _, item := range userWhitelist {
			if stringInStringSlice(item, removeWhiteList) {

			} else {
				newWhiteList = append(newWhiteList, item)
			}
		}
		newWhiteList = sliceUnique(newWhiteList)
		viper.Set("whitelist", newWhiteList)
		viper.WriteConfig()
		msg.Text = "whiteList removed"
	} else {
		msg.Text = "Item is empty"
	}

	bot.Send(msg)
}

func getLimits(bot *tgbotapi.BotAPI, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")

	limits := []string{"max_unfollow_per_day", "days_before_unfollow", "max_likes_to_account_per_session", "max_retry", "like.min", "like.count", "like.max", "follow.count", "follow.potency_ratio", "comment.min", "comment.count", "comment.max"}
	for _, limit := range limits {
		if limit == "follow.potency_ratio" {
			msg.Text += fmt.Sprintf("%s: %.2f\n", limit, viper.GetFloat64("limits."+limit))
		} else {
			msg.Text += limit + ": " + strconv.Itoa(viper.GetInt("limits."+limit)) + "\n"
		}
	}

	bot.Send(msg)
}

func updateLimits(bot *tgbotapi.BotAPI, limitStr string, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")
	s := strings.Split(limitStr, " ")
	limits := []string{"max_unfollow_per_day", "days_before_unfollow", "max_likes_to_account_per_session", "max_retry", "like.min", "like.count", "like.max", "follow.count", "follow.potency_ratio", "comment.min", "comment.count", "comment.max"}
	if len(s) != 2 {
		msg.Text = "/updatelimits limitname integer\nlimitname maybe one of: " + strings.Join(limits, ", ")
	} else {
		limit, count := s[0], s[1]

		if stringInStringSlice(limit, limits) {
			if limit == "follow.potency_ratio" {
				limitCount, _ := strconv.ParseFloat(count, 64)
				if limitCount >= -100 && limitCount <= 100 {
					viper.Set("limits."+limit, limitCount)
					viper.WriteConfig()
					msg.Text = "Limit updated"
				} else {
					msg.Text = "/updatelimits limitname float\ncount should be equal or greater than -100 and less or equal than 100"
				}
			} else {
				limitCount, _ := strconv.Atoi(count)
				if limitCount >= 0 && limitCount <= 10000 {
					viper.Set("limits."+limit, limitCount)
					viper.WriteConfig()
					msg.Text = "Limit updated"
				} else {
					msg.Text = "/updatelimits limitname integer\ncount should be equal or greater than 0 and less or equal than 10000"
				}
			}
		} else {
			msg.Text = "/updatelimits limitname integer\nlimitname maybe one of: " + strings.Join(limits, ", ")
		}
	}

	bot.Send(msg)
}

func updateProxy(bot *tgbotapi.BotAPI, proxyStr string, userID int64) {
	msg := tgbotapi.NewMessage(userID, "")

	if proxyStr == "" {
		viper.Set("user.instagram.proxy", "")
		viper.WriteConfig()
	} else {
		proxyURL, _ := url.Parse(proxyStr)

		timeout := time.Duration(5 * time.Second)
		httpClient := &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DisableKeepAlives: true,
				Proxy:             http.ProxyURL(proxyURL),
			},
		}
		response, err := httpClient.Get("https://api.ipify.org?format=json")

		if err != nil {
			msg.Text = fmt.Sprintf("bad proxy: %s", err)
			bot.Send(msg)
		} else {
			proxyStr = strings.TrimPrefix(proxyStr, "http://")
			proxyStr = strings.TrimPrefix(proxyStr, "https://")

			viper.Set("user.instagram.proxy", "http://"+proxyStr)
			viper.WriteConfig()
			msg.Text = "proxy updated, /relogin if needed"
			bot.Send(msg)

		}
		defer response.Body.Close()
	}
}

func getLastLikers() (result []string) {
	user, err := insta.Profiles.ByName(insta.Account.Username)
	if err != nil {
		log.Println(err)
	}

	latest := make([]goinsta.Item, 0)
	latestItems := user.Feed()
	for latestItems.Next() {
		for _, item := range latestItems.Items {
			latest = append(latest, item)
		}
	}

	l := latest
	if len(l) > 10 {
		l = latest[0:10] //last 10 posts
	}
	for lindex := range l {
		if l[lindex].Likes > 0 {
			l[lindex].SyncLikers()
			likers := l[lindex].Likers
			for _, item := range likers {
				if !stringInStringSlice(item.Username, result) {
					result = append(result, item.Username)
				}
			}
		}
	}

	return result
}

// Follows a user, if not following already
func followUser(tag string, db *bolt.DB, user goinsta.User) error {
	if !user.Friendship.Following {
		if !dev {
			if user.IsPrivate {
				log.Printf("%s is private, skipping follow\n", user.Username)
			} else {
				log.Printf("Following %s\n", user.Username)
				err := user.Follow()
				if err != nil {
					return err
				} else {
					user.Friendship.Following = true
				}
			}
		}

		if user.Friendship.Following {
			numFollowed++
			report[tag]["follow"]++
			incStats(db, "follow")
			setFollowed(db, user.Username)
		}
	} else {
		log.Println("Already following " + user.Username)
	}

	return nil
}

// Likes an image, if not liked already
func likeImage(tag string, db *bolt.DB, image goinsta.Item, userInfo goinsta.User) error {
	log.Println("Liking the picture https://www.instagram.com/p/" + image.Code)

	if !image.HasLiked {
		if !dev {
			err := image.Like()
			if err != nil {
				return err
			}
		}
		numLiked++

		report[tag]["like"]++
		incStats(db, "like")
		likesToAccountPerSession[userInfo.Username]++
	}

	return nil
}

// Comments an image
func commentImage(tag string, db *bolt.DB, image goinsta.Item) {
	// FIXME
	return

	// // rand.Seed(time.Now().Unix())
	// text := commentsList[rand.Intn(len(commentsList))]
	// if !dev {
	// 	// image.Comments.Sync()
	// 	err := image.Comments.Add(text)
	// 	if err != nil {
	// 		log.Println(err)
	// 		return
	// 	}
	// 	// insta.Comment(image.ID, text)
	// }
	// log.Println("Commented " + text)
	// numCommented++

	// report[tag]["comment"]++
	// incStats(db, "comment")
}

// Go through all the tags in the list
func startGeneralTask(name string, db *bolt.DB) error {
	usersInfo = make(map[string]goinsta.User)
	tagFeed = make(map[string]goinsta.Item)

	followStartedAt := time.Now()

	l.Lock()
	followStartedAt = time.Now()
	state["follow"] = 0
	reportAsString = ""
	l.Unlock()

	time.Sleep(1 * time.Second)

	report = make(map[string]map[string]int)
	likesToAccountPerSession = make(map[string]int)

	followTestUsername := viper.GetString("user.instagram.follow_test_username")
	if followTestUsername != "" {
		user, err := insta.Profiles.ByName(followTestUsername)

		if err != nil {
			log.Printf("test instagram username (%s) not found", followTestUsername)
		} else {
			if !dev {
				err := user.Follow()
				if err != nil {
					text := fmt.Sprintf("test user not followed, /follow canceled. %v", err)
					telegramResp <- telegramResponse{text, "follow"}

					return nil
				}

				user.Unfollow()
			}
			log.Printf("test instagram username (%s) followed and unfollowed", user.Username)
		}
	}

	var allCount = len(tagsList)
	if allCount > 0 {
		var current = 0

		shuffle(tagsList)
		for _, tag := range tagsList {
			l.RLock()
			if !isStarted[name] {
				l.RUnlock()
				return nil
			}
			l.RUnlock()

			report[tag] = make(map[string]int)
			report[tag]["like"] = 0
			report[tag]["follow"] = 0
			report[tag]["comment"] = 0

			current++

			l.Lock()
			state["follow"] = int(current * 100 / allCount)
			state["follow_current"] = current
			state["follow_all_count"] = allCount
			l.Unlock()

			numFollowed = 0
			numLiked = 0
			numCommented = 0

			reportAsString = fmt.Sprintf("[%d/%d] %d%%", state["follow_current"], state["follow_all_count"], state["follow"])
			if current > 1 {
				l.RLock()
				elapsed := time.Since(followStartedAt)
				l.RUnlock()
				perOne := elapsed.Seconds() / float64(current)
				duration := time.Duration(time.Duration(perOne*float64(allCount-current+1)) * time.Second)
				reportAsString += fmt.Sprintf(" ~%s", duration.Round(time.Second))
			}

			for tagItem := range report {
				if tagItem != tag {
					if report[tagItem]["like"] > 0 || report[tagItem]["follow"] > 0 || report[tagItem]["comment"] > 0 {
						reportAsString += fmt.Sprintf("\n#%s: %d 🐾, %d 👍, %d 💌", tagItem, report[tagItem]["follow"], report[tagItem]["like"], report[tagItem]["comment"])
					} else {
						reportAsString += fmt.Sprintf("\n#%s: no actions, possibly not enough images", tagItem)
					}
				}
			}

			reportAsString += fmt.Sprintf("\n#%s: ...", tag)

			telegramResp <- telegramResponse{reportAsString, "follow"}
			feedTag, err := insta.Feed.Tags(tag)

			if err != nil {
				log.Println(err)
			} else {
				l.RLock()
				if !isStarted[name] {
					l.RUnlock()
					return nil
				}
				l.RUnlock()

				log.Println("Fetching the list of images for #" + tag)

				var t = 1
				for _, item := range feedTag.Images {
					l.RLock()
					if !isStarted[name] {
						l.RUnlock()
						return nil
					}
					l.RUnlock()

					// Exiting the loop if there is nothing left to do
					if numFollowed >= followCount && numLiked >= likeCount && numCommented >= commentCount {
						break
					}

					// Skip our own images
					if item.User.Username == instaUsername {
						continue
					}

					// Check if we should fetch new images for tag
					if t >= followCount && t >= likeCount && t >= commentCount {
						break
					}

					if stringInStringSlice(item.User.Username, userWhitelist) {
						log.Printf("Skip following %s, in white list\n", item.User.Username)
						continue
					}

					// Getting the user info
					// Instagram will return a 500 sometimes, so we will retry 10 times.
					// Check retry() for more info.
					var posterInfo, ok = usersInfo[item.User.Username]
					if ok {
						// log.Println("from cache " + posterInfo.User.Username + " - for #" + tag)
					} else {
						err := retry(10, 20*time.Second, func() (err error) {
							posterNew, err := insta.Profiles.ByName(item.User.Username)
							if err == nil {
								usersInfo[item.User.Username] = *posterNew
								posterInfo = *posterNew
							}
							return
						})
						check(err)
					}

					poster := posterInfo
					followerCount := poster.FollowerCount
					likesCount := item.Likes
					commentsCount := item.CommentCount
					followingCount := poster.FollowingCount

					// Will only follow and comment if we like the picture
					like := numLiked < likeCount && !item.HasLiked
					follow := numFollowed < followCount && like
					comment := numCommented < commentCount && like

					var relationshipRatio float64 // = 0.0

					if followerCount != 0 && followingCount != 0 {
						relationshipRatio = float64(followingCount) / float64(followerCount)
					}

					// log.Println("Checking followers for " + poster.Username + " - for #" + tag)

					if followCount <= 0 {
						follow = false
					} else {
						if follow {
							if relationshipRatio == 0 || relationshipRatio < potencyRatio {
								log.Printf("%s is not a potential user with the relationship ratio of %.2f (%d/%d) ~skipping user\n", poster.Username, relationshipRatio, followingCount, followerCount)
								follow = false
							} else {
								log.Printf("%s with the relationship ratio of %.2f (%d/%d)\n", poster.Username, relationshipRatio, followingCount, followerCount)
							}
						}
					}

					if likeCount <= 0 {
						like = false
					} else {
						if likesCount > likeUpperLimit {
							log.Printf("%s's image has %d likes, more than max %d\n", poster.Username, likesCount, likeUpperLimit)
							like = false
						} else if likesCount < likeLowerLimit {
							log.Printf("%s's image has %d likes, less than min %d\n", poster.Username, likesCount, likeLowerLimit)
							like = false
						}
					}

					if commentCount <= 0 {
						comment = false
					} else {
						if commentsCount > commentUpperLimit {
							log.Printf("%s's image has %d comments, more than max %d\n", poster.Username, commentsCount, commentUpperLimit)
							comment = false
						} else if commentsCount < commentLowerLimit {
							log.Printf("%s's image has %d comments, less than min %d\n", poster.Username, commentsCount, commentLowerLimit)
							comment = false
						}
					}

					if like || comment || follow {
						// log.Printf("%s has %d followers\n", poster.Username, followerCount)

						if relationshipRatio >= potencyRatio {
							t++
							// Like, then comment/follow
							if like {
								if userLikesCount, ok := likesToAccountPerSession[posterInfo.Username]; ok {
									if userLikesCount < maxLikesToAccountPerSession {
										err := likeImage(tag, db, item, posterInfo)
										if err != nil {
											log.Println(err)
											return err
										}
										// item.HasLiked = true
									} else {
										log.Println("Likes count per user reached [" + poster.Username + "]")
									}
								} else {
									err := likeImage(tag, db, item, posterInfo)
									if err != nil {
										log.Println(err)
										return err
									}
								}

								previoslyFollowed, _ := getFollowed(db, posterInfo.Username)
								if previoslyFollowed != "" {
									log.Printf("%s already following (%s), skipping\n", posterInfo.Username, previoslyFollowed)
								} else {
									if comment {
										if !item.HasLiked {
											commentImage(tag, db, item)
										}
									}
									if follow {
										err := followUser(tag, db, posterInfo)
										if err != nil {
											log.Println(err)
											return err
										}
									}
								}
							}
						}
					} else {
						log.Printf("%s, nothing to do\n", poster.Username)
					}

					reportAsString = fmt.Sprintf("[%d/%d] %d%%", state["follow_current"], state["follow_all_count"], state["follow"])
					for tag := range report {
						if report[tag]["like"] > 0 || report[tag]["follow"] > 0 || report[tag]["comment"] > 0 {
							reportAsString += fmt.Sprintf("\n#%s: %d 🐾, %d 👍, %d 💌", tag, report[tag]["follow"], report[tag]["like"], report[tag]["comment"])
						} else {
							reportAsString += fmt.Sprintf("\n#%s: ...", tag)
						}
					}

					telegramResp <- telegramResponse{reportAsString, "follow"}

					l.RLock()
					if !isStarted[name] {
						l.RUnlock()
						return nil
					}
					l.RUnlock()

					// This is to avoid the temporary ban by Instagram
					time.Sleep(17 * time.Second)

					feedTag.Next()
				}

				if current != allCount {
					reportAsString += fmt.Sprintf("\n... sleep %d seconds", 10)
				}

				telegramResp <- telegramResponse{reportAsString, "follow"}

				if current != allCount {
					time.Sleep(10 * time.Second)
				}

				l.RLock()
				if !isStarted[name] {
					l.RUnlock()
					return nil
				}
				l.RUnlock()
			}
		}

		l.RLock()
		elapsed := time.Since(followStartedAt)
		l.RUnlock()

		if reportAsString != "" {
			reportAsString += fmt.Sprintf("\n\nFollowing is finished by %s", elapsed.Round(time.Second))
		} else {
			reportAsString = "Follow finished"
		}

		telegramResp <- telegramResponse{reportAsString, "follow"}

		l.Lock()
		state["follow"] = -1
		reportAsString = ""
		l.Unlock()
	}

	return nil
}

func startUnFollowFromQueue(name string, db *bolt.DB, limit int) error {
	var maxLimit = viper.GetInt("limits.max_unfollow_per_day")

	today, _ := getStats(db, "unfollow")
	if maxLimit == 0 || (maxLimit-today) <= 0 {
		log.Println("today unfollow limit reached")
		return nil
	}

	var current = 0
	usersQueue := getListFromQueue(db, "unfollowqueue", limit)
	for index := range usersQueue {
		l.RLock()
		if !isStarted[name] {
			l.RUnlock()

			telegramResp <- telegramResponse{fmt.Sprintf("\nUnfollowed finished: %s", current), "unfollow"}

			return nil
		}
		l.RUnlock()

		current++
		user, err := insta.Profiles.ByName(usersQueue[index])
		if err != nil {
			log.Printf("[%d/%d] %s doesn't exist\n", current, limit, usersQueue[index])
			deleteByKey(db, "unfollowqueue", usersQueue[index])
			continue
		}

		log.Printf("[%d/%d] Unfollowing %s\n", current, limit, usersQueue[index])
		err = user.Unfollow()
		if err != nil {
			log.Println(err)
			return nil
		} else {
			incStats(db, "unfollow")
		}

		deleteByKey(db, "unfollowqueue", usersQueue[index])

		today, _ := getStats(db, "unfollow")
		if (maxLimit - today) <= 0 {
			return nil
		}

		time.Sleep(time.Duration(3600/(limit+1)) * time.Second)
	}
	log.Println("unfollow finished")

	return nil
}

func updateUnfollowList(name string, db *bolt.DB) error {
	user, err := insta.Profiles.ByName(insta.Account.Username)
	if err != nil {
		log.Println(err)
	}

	following := make([]goinsta.User, 0)
	usersFollowing := user.Following()
	for usersFollowing.Next() {
		for _, user := range usersFollowing.Users {
			l.RLock()
			if !isStarted[name] {
				l.RUnlock()
				return nil
			}
			l.RUnlock()

			following = append(following, user)
		}
	}

	time.Sleep(600 * time.Second)

	followers := make([]goinsta.User, 0)
	usersFollowers := user.Followers()
	for usersFollowers.Next() {
		for _, user := range usersFollowers.Users {
			l.RLock()
			if !isStarted[name] {
				l.RUnlock()
				return nil
			}
			l.RUnlock()

			followers = append(followers, user)
		}
	}

	time.Sleep(600 * time.Second)

	var daysBeforeUnfollow = viper.GetInt("limits.days_before_unfollow")
	if daysBeforeUnfollow <= 0 || daysBeforeUnfollow >= 30 {
		daysBeforeUnfollow = 3
	}

	var users []goinsta.User
	for index := range following {
		l.RLock()
		if !isStarted[name] {
			l.RUnlock()
			return nil
		}
		l.RUnlock()

		if !contains(followers, following[index]) {
			previoslyFollowed, _ := getFollowed(db, following[index].Username)
			if previoslyFollowed != "" {
				t, err := time.Parse("20060102", previoslyFollowed)

				if err != nil {
					fmt.Println(err)
				} else {
					duration := time.Since(t)
					if int(duration.Hours()) < (24 * daysBeforeUnfollow) {
						fmt.Printf("%s not followed us less then %f hours, skipping!\n", following[index].Username, duration.Hours())
						continue
					} else {
						users = append(users, following[index])
					}
				}
			} else {
				users = append(users, following[index])
			}
		}
	}

	time.Sleep(600 * time.Second)

	lastLikers := getLastLikers()
	if len(lastLikers) > 0 {
		if len(following) > 0 {
			var notLikers []goinsta.User
			for index := range following {
				l.RLock()
				if !isStarted[name] {
					l.RUnlock()
					return nil
				}
				l.RUnlock()

				if !stringInStringSlice(following[index].Username, lastLikers) {
					notLikers = append(notLikers, following[index])
				}
			}

			if len(notLikers) > 0 {
				for index := range notLikers {
					l.RLock()
					if !isStarted[name] {
						l.RUnlock()
						return nil
					}
					l.RUnlock()

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

	for index := range users {
		addToQueue(db, "unfollowqueue", users[index].Username)
	}

	return nil
}

func likeFollowersPosts(db *bolt.DB) {
	return
}
