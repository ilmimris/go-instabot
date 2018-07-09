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

	// Setup the users bucket.
	_, err = tx.CreateBucketIfNotExists([]byte("stats"))
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
			return errors.Wrapf(fmt.Errorf("key not found"), "failed to find stats for 's'", id)
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
