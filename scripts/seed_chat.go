//go:build ignore

package main

import (
	"log"

	"github.com/b1rd33/tgctl-go/internal/store"
)

func main() {
	db, err := store.Connect("./accounts/default/telegram.sqlite")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT OR REPLACE INTO tg_chats(chat_id,title,username,type) VALUES (1,'Test','tg','user')"); err != nil {
		log.Fatal(err)
	}
	println("seeded chat_id=1")
}
