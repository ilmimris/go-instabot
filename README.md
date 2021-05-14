[![License: GPL v3](https://img.shields.io/badge/License-GPL%20v3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0) [![Made with: Golang](https://img.shields.io/badge/Made%20with-Golang-brightgreen.svg)](https://golang.org/)

[![Run on Repl.it](https://repl.it/badge/github/tducasse/go-instabot)](https://repl.it/github/tducasse/go-instabot)

### Not actively maintained, feel free to fork 👍

# What is go-instabot?

The easiest way to boost your Instagram account and get likes and followers.

Go-instabot automates **following** users, **liking** pictures, **commenting**, and **unfollowing** people that don't follow you back on Instagram.

![Instabot demo gif](/docs/instabot.gif)

### Concept
The idea behind the script is that when you like, follow, or comment something, it will draw the user's attention back to your own account. There's a hidden convention in Instagram, that will make people follow you back, as a way of saying "thank you" I guess.
Moreover, you may have noticed that when you follow someone, Instagram tells you about 'similar people to follow'. The more active you are on Instagram, the more likely you are to be in this section.

# How to use
## Installation

1. [Install Go](https://golang.org/doc/install) on your system.

2. Download and install go-instabot, by executing this command in your terminal / cmd :

    `brew install dep`
    `go get -u github.com/ad/go-instabot`

## Configuration
### Config.json
Go to the project folder :

`cd [YOUR_GO_PATH]/src/github.com/tducasse/go-instabot`

For your telegram id ask [@myidbot](https://t.me/myidbot), for bot token ask [@BotFather](https://t.me/BotFather).

Commands list for BotFather:
 - stats - статистика за день
 - progress - текущий прогресс запущенных задач
 - follow - запустить задачи по подписке/лайкам/комментам
 - refollow - подписаться на подписчиков @...
 - cancelfollow - остановить задачу подписок
 - cancelrefollow - прекратить подписку на подписчиков пользователя
 - cancelfollowLikers - прекратить подписку на тех, кому понравился пост
 - getcomments - список комментов для отправки
 - addcomments - добавить комменты (через ", ")
 - removecomments - удалить комменты (через ", ")
 - gettags - список тэгов для /follow
 - addtags - добавить тэги (через ", ")
 - removetags - удалить тэги (через ", ")
 - getlimits - получить список значений лимитов
 - updatelimits - установить значение лимита
 - followLikers post url - подписаться на тех, кому понравился пост 

There, in the 'dist/' folder, you will find a sample 'config.json', that you have to copy to the 'config/' folder :

```go
{
    "user": {
        "instagram": {
            "username": "foobar",
            "password": "fooBAR"
        },
        "telegram": {
            "admins": [
                123,
                321
            ],
            "reportID": 123,
            "token": ".....:......._....."
        }
    },
    "limits": {
        "max_unfollow_per_day": 1000,
        "days_before_unfollow": 2,
        "max_likes_to_account_per_session": 3,
        "max_retry": 2,
        "like": {
            "min": 0,
            "count": 20,
            "max": 10000
        },
        "comment": {
            "min": 100,
            "count": 2,
            "max": 10000
        },
        "follow": {
            "min": 200,
            "count": 10,
            "max": 10000
        }
    },
    "tags" : {                              // this is the list of hashtags you want to explore
        "dog" : {                           // do not put the '#' symbol
            "like" : 3,                     // the number you want to like
            "comment" : 2,                  // the number you want to comment
            "follow" : 1                    // the number you want to follow
        },
        "cat" : {                           // another hashtag ('#cat')
            "like" : 3,
            "comment" : 2,
            "follow" : 1
        }                                   // following these examples, add as many as you want
    },
    "comments" : [                          // the script will take the comments from the following list
        "awesome",                          // again, add as many as you want
        "wow",                              // it will randomly choose one 
        "nice pic"                          // each time it has to put a comment
    ],
    "blacklist" : [                         // a list of users you don't want to follow
        "foo",                          
        "bar",                              // the scripts prompts you to choose whether to unfollow them or not
        "foobar",                           // when you use -sync
        "barfoo"                            // This list will be updated at the end of the script.
    ],
    "whitelist" : [                         // a list of users you don't want to unfollow
        "boo",                          
        "far",                              // it adds them to the whitelist if you choose to answer "No" (N)
        "boofar",                           // the scripts prompts you to choose whether to unfollow them or not
        "farboo"                            // This list will be updated at the end of the script.
    ]                                       // The list might become obsolete as username is easily changeable by user.
}
```	

## How to run
This is it!
Since you used the `go get` command, you now have the `go-instabot` executable available from anywhere* in your system. Just launch it in a terminal :

`go-instabot`

**\*** : *You will need to have a folder named 'config' (with a 'config.json' file) in the directory where you launch it.*

### Options
**-h** : Use this option to display the list of options.

**-config** : Path to config file. config/config.json by default.

**-logs** : Use this option to enable the logfile. The script will continue writing everything on the screen, but it will also write it in a .log file.

**-nomail** : Use this option to disable the email notifications.

**-sync** : Use this option to unfollow users that don't follow you back. Don't worry, the script will ask before actually doing it, so you can use it just to check the number!

**-noduplicate** : Use this to skip following, liking and commenting same user in this session!

### Tips
- If you want to launch a long session, and you're afraid of closing the terminal, I recommend using the command __screen__.
- If you have a Raspberry Pi, a web server, or anything similar, you can run the script on it (again, use screen).
- To maximize your chances of getting new followers, don't spam! If you follow too many people, you will become irrelevant.

  Also, try to use hashtags related to your own account : if you are a portrait photographer and you suddenly start following a thousand #cats related accounts, I doubt it will bring you back a thousand new followers...
  
Good luck getting new followers!

### ⚠️ Reporting issues/PRs/license
This is _very_ loosely maintained, as in, I'll _probably_ try and fix things if everything is broken, but I'm no longer working on it. Feel free to fork it though!
