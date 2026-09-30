package main

import "github.com/z46-dev/golog"

// Note: any log.<level>f() method requires you to put a \n at the end of the string otherwise it will not go to a new line.
// Format methods allow you to be more specific.
var log *golog.Logger = golog.New().Prefix("[MAIN]", golog.BoldBlue).Timestamp()

func main() {
	log.Info("Starting...")
}
