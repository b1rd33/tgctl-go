//go:build ignore

package main

import (
	"log"
	"os"

	"github.com/b1rd33/tgctl-go/internal/store"
)

func main() {
	dbPath := os.Getenv("TGCTL_TEST_DB")
	if dbPath == "" {
		log.Fatal("TGCTL_TEST_DB must point to a disposable test database")
	}
	db, err := store.Connect(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT OR REPLACE INTO tg_chats(chat_id,title,username,type) VALUES (1,'Test','tg','user')"); err != nil {
		log.Fatal(err)
	}
	println("seeded chat_id=1")
}
