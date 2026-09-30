package main

import (
	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/config"
	"github.com/z46-dev/organesson/backend/db"
)

// Note: any log.<level>f() method requires you to put a \n at the end of the string otherwise it will not go to a new line.
// Format methods allow you to be more specific.
var log *golog.Logger = golog.New().Prefix("[MAIN]", golog.BoldBlue).Timestamp()

func main() {
	log.Info("Starting...")

	var err error

	if err = config.Init("config.toml"); err != nil {
		log.Panicf("Failed to load configuration: %v\n", err)
	}

	if err = db.Init(log); err != nil {
		log.Panicf("Failed to initialize database: %v\n", err)
	}
}
