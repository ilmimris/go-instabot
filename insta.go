package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
	"github.com/tducasse/goinsta"
	"github.com/tducasse/goinsta/response"
	"github.com/tducasse/goinsta/store"
)

// Insta is a goinsta.Instagram instance
var insta *goinsta.Instagram

// login will try to reload a previous session, and will create a new one if it can't
func login() {
	err := reloadSession()
	if err != nil {
		createAndSaveSession()
	}
}

func syncFollowers() {
	for msg := range unfollow_req {
		following, err := insta.SelfTotalUserFollowing()
		check(err)
		followers, err := insta.SelfTotalUserFollowers()
		check(err)

		var users []response.User
		for _, user := range following.Users {
			if !contains(followers.Users, user) {
				users = append(users, user)
			}
		}

		var all_count = int(math.Max(float64(len(users)), 1000))
		if all_count > 0 {
			var current = 0

			fmt.Printf("\n%d users are not following you back!\n", all_count)
			unfollow_res <- UnfollowResponse{fmt.Sprintf("%d will be unfollowed", all_count), msg}

			for _, user := range users {
				current += 1
				if current >= 1000 {
					continue
				}

				mutex.Lock()
				if state["unfollow_cancel"] > 0 {
					state["unfollow_cancel"] = 0
					state["unfollow"] = -1
					mutex.Unlock()
					unfollow_res <- UnfollowResponse{"Unfollowing canceled", msg}
					return
				}
				state["unfollow"] = int(current * 100 / all_count)
				state["unfollow_current"] = current
				state["unfollow_all_count"] = all_count

				mutex.Unlock()

				fmt.Printf("[%d/%d] Unfollowing %s (%d%%)\n", state["unfollow_current"], state["unfollow_all_count"], user.Username, state["unfollow"])
				if !*dev {
					insta.UnFollow(user.ID)
				}
				time.Sleep(10 * time.Second)
			}

			mutex.Lock()
			state["unfollow"] = -1
			mutex.Unlock()

			unfollow_res <- UnfollowResponse{fmt.Sprintf("\nUnfollowed %d users are not following you back!\n", all_count), msg}
		}
	}
}

func getInput(text string) string {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(text)

	input, err := reader.ReadString('\n')
	check(err)
	return strings.TrimSpace(input)
}

// Checks if the user is in the slice
func contains(slice []response.User, user response.User) bool {
	for _, currentUser := range slice {
		if currentUser == user {
			return true
		}
	}
	return false
}

// Logins and saves the session
func createAndSaveSession() {
	insta = goinsta.New(viper.GetString("user.instagram.username"), viper.GetString("user.instagram.password"))
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

// Go through all the tags in the list
func loopTags() {
	for msg := range follow_req {
		var all_count = len(tagsList)
		if all_count > 0 {
			var current = 0

			for tag = range tagsList {
				current += 1
				mutex.Lock()
				if state["follow_cancel"] > 0 {
					state["follow_cancel"] = 0
					state["follow"] = -1
					mutex.Unlock()
					follow_res <- FollowResponse{"Following canceled", msg}
					return
				}

				state["follow"] = int(current * 100 / all_count)
				state["follow_current"] = current
				state["follow_all_count"] = all_count
				mutex.Unlock()

				limitsConf := viper.GetStringMap("tags." + tag)
				// Some converting
				limits = map[string]int{
					"follow":  int(limitsConf["follow"].(float64)),
					"like":    int(limitsConf["like"].(float64)),
					"comment": int(limitsConf["comment"].(float64)),
				}
				// What we did so far
				numFollowed = 0
				numLiked = 0
				numCommented = 0

				browse()
			}
		}
		mutex.Lock()
		state["follow"] = -1
		mutex.Unlock()

		reportAsString := ""
		for index, element := range report {
			if index.Action == "like" {
				reportAsString += fmt.Sprintf("#%s liked: %d\n", index.Tag, element)
			} else {
				reportAsString += fmt.Sprintf("#%s %sed: %d\n", index.Tag, index.Action, element)
			}
		}

		// Displays the report on the screen / log file
		fmt.Println(reportAsString)

		// Sends the report to the email in the config file, if the option is enabled
		follow_res <- FollowResponse{reportAsString, msg}
	}
}

// Browses the page for a certain tag, until we reach the limits
func browse() {
	var i = 0
	for numFollowed < limits["follow"] || numLiked < limits["like"] || numCommented < limits["comment"] {
		log.Println("Fetching the list of images for #" + tag + "\n")
		i++

		// Getting all the pictures we can on the first page
		// Instagram will return a 500 sometimes, so we will retry 10 times.
		// Check retry() for more info.
		var images response.TagFeedsResponse
		err := retry(10, 20*time.Second, func() (err error) {
			images, err = insta.TagFeed(tag)
			return
		})
		check(err)

		goThrough(images)

		if viper.IsSet("limits.maxRetry") && i > viper.GetInt("limits.maxRetry") {
			log.Println("Currently not enough images for this tag to achieve goals")
			break
		}
	}
}

// Goes through all the images for a certain tag
func goThrough(images response.TagFeedsResponse) {
	var i = 1
	for _, image := range images.FeedsResponse.Items {
		// Exiting the loop if there is nothing left to do
		if numFollowed >= limits["follow"] && numLiked >= limits["like"] && numCommented >= limits["comment"] {
			break
		}

		// Skip our own images
		if image.User.Username == viper.GetString("user.instagram.username") {
			continue
		}

		// Check if we should fetch new images for tag
		if i >= limits["follow"] && i >= limits["like"] && i >= limits["comment"] {
			break
		}

		// Getting the user info
		// Instagram will return a 500 sometimes, so we will retry 10 times.
		// Check retry() for more info.
		var posterInfo response.GetUsernameResponse
		err := retry(10, 20*time.Second, func() (err error) {
			posterInfo, err = insta.GetUserByID(image.User.ID)
			return
		})
		check(err)

		poster := posterInfo.User
		followerCount := poster.FollowerCount

		buildLine()

		log.Println("Checking followers for " + poster.Username + " - for #" + tag)
		log.Printf("%s has %d followers\n", poster.Username, followerCount)
		i++

		// Will only follow and comment if we like the picture
		like := followerCount > likeLowerLimit && followerCount < likeUpperLimit && numLiked < limits["like"]
		follow := followerCount > followLowerLimit && followerCount < followUpperLimit && numFollowed < limits["follow"] && like
		comment := followerCount > commentLowerLimit && followerCount < commentUpperLimit && numCommented < limits["comment"] && like

		// Like, then comment/follow
		if like {
			likeImage(image)
			if follow {
				followUser(posterInfo)
			}
			if comment {
				commentImage(image)
			}
		}
		log.Printf("%s done\n\n", poster.Username)

		// This is to avoid the temporary ban by Instagram
		time.Sleep(20 * time.Second)
	}
}

// Likes an image, if not liked already
func likeImage(image response.MediaItemResponse) {
	log.Println("Liking the picture https://www.instagram.com/p/" + image.Code)
	if !image.HasLiked {
		if !*dev {
			insta.Like(image.ID)
		}
		log.Println("Liked")
		numLiked++
		report[line{tag, "like"}]++
	} else {
		log.Println("Image already liked")
	}
}

// Comments an image
func commentImage(image response.MediaItemResponse) {
	rand.Seed(time.Now().Unix())
	text := commentsList[rand.Intn(len(commentsList))]
	if !*dev {
		insta.Comment(image.ID, text)
	}
	log.Println("Commented " + text)
	numCommented++
	report[line{tag, "comment"}]++
}

// Follows a user, if not following already
func followUser(userInfo response.GetUsernameResponse) {
	user := userInfo.User
	log.Printf("Following %s\n", user.Username)
	userFriendShip, err := insta.UserFriendShip(user.ID)
	check(err)
	// If not following already
	if !userFriendShip.Following {
		if !*dev {
			insta.Follow(user.ID)
		}
		log.Println("Followed")
		numFollowed++
		report[line{tag, "follow"}]++
	} else {
		log.Println("Already following " + user.Username)
	}
}
