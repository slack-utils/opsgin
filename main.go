package main

import (
	"log"

	oc "github.com/slack-utils/opsgin-core"
)

var key string

func main() {
	if err := oc.Run(key); err != nil {
		log.Fatal(err)
	}
}
