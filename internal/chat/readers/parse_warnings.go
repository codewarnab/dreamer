package readers

import "log"

func logDroppedParseRecords(readerName string, filePath string, count int) {
	if count == 0 {
		return
	}
	log.Printf("dreamer: %s dropped %d malformed record(s) while reading %q", readerName, count, filePath)
}
