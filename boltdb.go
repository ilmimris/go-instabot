package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/boltdb/bolt"
	"github.com/pkg/errors"
)

func initBolt() (db *bolt.DB, error error) {
	db, err := bolt.Open("instabot.db", 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return
	}

	tx, err := db.Begin(true)
	if err != nil {
		return
	}
	defer tx.Rollback()

	// Setup the stats bucket.
	_, err = tx.CreateBucketIfNotExists([]byte("stats"))
	if err != nil {
		return
	}

	// Setup the followed bucket.
	_, err = tx.CreateBucketIfNotExists([]byte("followed"))
	if err != nil {
		return
	}

	if err := tx.Commit(); err != nil {
		return
	}

	return db, nil
}

func getStats(db *bolt.DB, id string) (int, error) {
	d := time.Now().Format("20060102")
	id = d + id

	var count int
	err := db.View(func(tx *bolt.Tx) error {
		bk := tx.Bucket([]byte("stats"))
		if bk == nil {
			return errors.Wrapf(fmt.Errorf("failed to find bucket"), "failed to get 'stats' bucket")
		}

		bs := bk.Get([]byte(id))
		if bs == nil {
			return errors.Wrapf(fmt.Errorf("key not found"), "failed to find stats for '%s'", id)
		}

		var err error
		count, err = strconv.Atoi(string(bs))
		if err != nil {
			return errors.Wrapf(fmt.Errorf("stat count is not a number"), "invalid stat value for '%s'", id)
		}

		return nil
	})
	return count, err
}

func incStats(db *bolt.DB, id string) error {
	d := time.Now().Format("20060102")
	id = d + id

	err := db.Update(func(tx *bolt.Tx) error {
		bk, err := tx.CreateBucketIfNotExists([]byte("stats"))
		if err != nil {
			return errors.Wrapf(fmt.Errorf("failed to find bucket"), "failed to get 'stats' bucket")
		}

		var count int

		bs := bk.Get([]byte(id))
		if bs == nil {
			fmt.Printf("No previous stats found for '%s'\n", id)
		} else {
			count, err = strconv.Atoi(string(bs))
			if err != nil {
				return errors.Wrapf(fmt.Errorf("stat count is not a number"), "invalid stat value for '%s'", id)
			}
		}

		count = count + 1
		countStr := strconv.Itoa(count)

		return bk.Put([]byte(id), []byte(countStr))
	})
	return err
}

func getFollowed(db *bolt.DB, id string) (string, error) {
	var prevDate string
	err := db.View(func(tx *bolt.Tx) error {
		bk := tx.Bucket([]byte("followed"))
		if bk == nil {
			return errors.Wrapf(fmt.Errorf("failed to find bucket"), "failed to get 'followed' bucket")
		}

		bs := bk.Get([]byte(id))
		if bs == nil {
			return nil
		}
		prevDate = string(bs)

		return nil
	})
	return prevDate, err
}

func setFollowed(db *bolt.DB, id string) error {
	d := time.Now().Format("20060102")
	db.Update(func(tx *bolt.Tx) error {
		bk := tx.Bucket([]byte("followed"))

		err := bk.Put([]byte(id), []byte(d))
		if err != nil {
			return err
		}
		return nil
	})
	return nil
}

func getListFromQueue(db *bolt.DB, queuename string, limit int) []string {
	var usersQueue []string
	var current = 0
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(queuename))
		if b == nil {
			return errors.Wrapf(fmt.Errorf("failed to find bucket"), "failed to get '"+queuename+"' bucket")
		}

		c := b.Cursor()

		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if current < limit {
				usersQueue = append(usersQueue, string(k))
				//fmt.Printf("key=>[%s], value=[%s]\n", k, v)
				current++
			} else {
				break
			}
		}
		return nil
	})

	if err != nil {
		fmt.Printf("failure : %s\n", err)
	}
	return usersQueue
}

func getItemFromQueue(db *bolt.DB, queuename string, key string) (value string) {
	db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(queuename))
		if b == nil {
			return errors.Wrapf(fmt.Errorf("failed to find bucket"), "failed to get '"+queuename+"' bucket")
		}

		if v := b.Get([]byte(key)); v != nil {
			value = string(v)
			return nil
		}
		return nil
	})
	return value
}

func addToQueue(db *bolt.DB, queuename string, username string) {
	date := time.Now().Format("20060102")
	updateDB(db, []byte(queuename), []byte(username), []byte(date))
}

func deleteByKey(db *bolt.DB, bucketName, key string) error {
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketName)).Delete([]byte(key))
	}); err != nil {
		return err
	}
	return nil
}

// updateDB : store data
func updateDB(db *bolt.DB, bucketName, key, value []byte) error {
	err := db.Update(func(tx *bolt.Tx) error {
		bkt, err := tx.CreateBucketIfNotExists(bucketName)
		if err != nil {
			return err
		}
		err = bkt.Put(key, value)
		if err != nil {
			return err
		}
		if err := db.Sync(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	return nil
}
