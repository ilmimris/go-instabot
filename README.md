[![License: GPL v3](https://img.shields.io/badge/License-GPL%20v3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0) [![Made with: Golang](https://img.shields.io/badge/Made%20with-Golang-brightgreen.svg)](https://golang.org/)
# What is go-instabot?

The easiest way to boost your Instagram account and get likes and followers.

Go-instabot automates **following** users, **liking** pictures, **commenting**, and **unfollowing** people that don't follow you back on Instagram.

It uses the unofficial but excellent Go Instagram API, [goinsta](https://github.com/ahmdrz/goinsta) (actually, a [frozen v1 fork](https://github.com/tducasse/goinsta)).

![Instabot demo gif](/docs/instabot.gif)

### Concept
The idea behind the script is that when you like, follow, or comment something, it will draw the user's attention back to your own account. There's a hidden convention in Instagram, that will make people follow you back, as a way of saying "thank you" I guess.

Moreover, you may have noticed that when you follow someone, Instagram tells you about 'similar people to follow'. The more active you are on Instagram, the more likely you are to be in this section.


# How does it work?
## Algorithm
- There is a config file, where you can enter hashtags, and for each hashtag, how many likes, comments, and follows you want.
- The script will then fetch pictures from the 'explore by tags' page on Instagram, using the API.
- It will decide (based on your settings) if it has to follow, like or comment.
- At the end of the routine, an email can be sent, with a report on everything that's been done.

Additionally, there is a retry mechanism in the eventuality that Instagram is too slow to answer (it can happen sometimes), and the script will wait for some time before trying again.

## Bans
The script is coded so that your Instagram account will not get banned ; it waits between every call to simulate human behavior.

The first time it logs you in, it will store the session object in a file, encrypted with AES (thanks to [goinsta](https://github.com/ahmdrz/goinsta)). Every next launch will try to recover the session instead of logging in again. This is a trick to avoid getting banned for connecting on too many devices (as suggested by [anastalaz](https://github.com/tducasse/go-instabot/issues/1)).

# How to use
## Installation

1. [Install Go](https://golang.org/doc/install) on your system.

2. Download and install go-instabot, by executing this command in your terminal / cmd :

    `go get github.com/tducasse/go-instabot`
    `go get github.com/constabulary/gb/...`
    `cd ~/go/src/github.com/ad/go-instabot/`
    `gb vendor restore`
    `gb build`
    `~/go/src/github.com/ad/go-instabot/bin/main`

## Configuration
### Config.json
Go to the project folder :

`cd [YOUR_GO_PATH]/src/github.com/tducasse/go-instabot`

For your telegram id ask [@getidsbot](https://t.me/getidsbot), for bot token ask [@BotFather](https://t.me/BotFather).

Commands list for BotFather:
 - stats - статистика за день
 - progress - текущий прогресс запущенных задач
 - follow - запустить задачи по подписке/лайкам/комментам
 - unfollow - отписаться от тех кто не подписан на нас
 - refollow - подписаться на подписчиков @...
 - cancelfollow - остановить задачу подписок
 - cancelunfollow - остановить задачу отписок
 - cancelrefollow - прекратить подписку на подписчиков пользователя
 - getcomments - список комментов для отправки
 - addcomments - добавить комменты (через ", ")
 - removecomments - удалить комменты (через ", ")
 - gettags - список тэгов для /follow
 - addtags - добавить тэги (через ", ")
 - removetags - удалить тэги (через ", ")
 - getlimits - получить список значений лимитов
 - updatelimits - установить значение лимита

There, in the 'dist/' folder, you will find a sample 'config.json', that you have to copy to the 'config/' folder :

```go
{
    "user" : {
        "instagram" : {
            "username" : "foobar",          // your instagram username
            "password" : "fooBAR"           // your instagram password
        },
        "telegram" : {          
            "id" : 123,                   // bot owner ID
            "token" : ".....:....._.....",// bot token
        }
    },
    "limits" : {                            // this sets when the script will choose to do something
        "maxRetry" : 2,                     // OPTIONAL: Maximum number of times th script will go through the same tag
        "like" : {                          // the script will like a picture only if :
            "min" : 0,                      //      - the user has more than 0 followers
            "max" : 10000                   //      - the user has less than 10000 followers  
        },
        "comment" : {                       // the script will comment only if :
            "min" : 100,                    //      - the user has more than 100 followers
            "max" : 10000                   //      - the user has less than 10000 followers  
        },
        "follow" : {                        // the script will follow someone only if :
            "min" : 200,                    //      - the user has more than 200 followers
            "max" : 10000                   //      - the user has less than 10000 followers  
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
    ]
}
```

## How to run
This is it!
Since you used the `go get` command, you now have the `go-instabot` executable available from anywhere* in your system. Just launch it in a terminal :

`go-instabot -run`

**\*** : *You will need to have a folder named 'config' (with a 'config.json' file) in the directory where you launch it.*

### Options
**-h** : Use this option to display the list of options.

**-dev** : Use this option to use the script in development mode : nothing will be done for real. You will need to put a config file in a 'local' folder.

**-logs** : Use this option to enable the logfile. The script will continue writing everything on the screen, but it will also write it in a .log file.

### Tips
- If you want to launch a long session, and you're afraid of closing the terminal, I recommend using the command __screen__.
- If you have a Raspberry Pi, a web server, or anything similar, you can run the script on it (again, use screen).
- To maximize your chances of getting new followers, don't spam! If you follow too many people, you will become irrelevant.

  Also, try to use hashtags related to your own account : if you are a portrait photographer and you suddenly start following a thousand #cats related accounts, I doubt it will bring you back a thousand new followers...
  
Good luck getting new followers!

