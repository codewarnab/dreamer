package nodirectlogtest

import "log"

func bad() {
	log.Printf("hello %s", "world") // want "direct use of log.Printf; use logging.Logger instead"
	log.Fatal("bye")                // want "direct use of log.Fatal; use logging.Logger instead"
	log.Println("info")             // want "direct use of log.Println; use logging.Logger instead"
}
