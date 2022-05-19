package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"
)

const (
	fileName = ".audit-count"

	// @TODO Make Params

	// Path to the venv with the cic command
	venvpath = "/Users/chrismarslender/Projects/internal-custody/venv"
	// datadir is the directory the commands will be run in and the sqlite, etc DB will be stored in
	datadir = "/Users/chrismarslender/Projects/prefarm-alert"
	// datafile is the file that contains the info about the singleton
	datafile = "Observer Info.txt"

	loopDelay = 30 * time.Second

	testAlertURL = ""
)

type auditItem struct {
	Time   int64           `json:"time"` // Unix Timestamp
	Action string          `json:"action"`
	Params json.RawMessage `json:"params"`
}

func main() {
	currentCount := loadLastKnownCount()

	// Check if CHIA_ROOT is set
	chiaRoot, chiaRootSet := os.LookupEnv("CHIA_ROOT")

	for {
		// 1. Call the sync command, and check for any errors
		syncCmd := exec.Command(fmt.Sprintf("%s/bin/cic", venvpath), "sync", "-c", datafile)
		syncCmd.Dir = datadir
		syncCmd.Env = append(syncCmd.Env, fmt.Sprintf("PATH=%s/bin/", venvpath))
		if chiaRootSet {
			syncCmd.Env = append(syncCmd.Env, fmt.Sprintf("CHIA_ROOT=%s", chiaRoot))
		}
		_, err := syncCmd.Output()

		if err != nil {
			// @TODO increment error counter
			log.Printf("Error running sync command: %s\n", err.Error())
			time.Sleep(loopDelay)
			continue
		}

		// 2. Call the audit command
		auditCmd := exec.Command(fmt.Sprintf("%s/bin/cic", venvpath), "audit")
		auditCmd.Dir = datadir
		auditCmd.Env = append(syncCmd.Env, fmt.Sprintf("PATH=%s/bin/", venvpath))
		if chiaRootSet {
			auditCmd.Env = append(syncCmd.Env, fmt.Sprintf("CHIA_ROOT=%s", chiaRoot))
		}
		auditJSON, err := auditCmd.Output()

		if err != nil {
			// @TODO increment error counter
			log.Printf("Error running audit command: %s\n", err.Error())
			time.Sleep(loopDelay)
			continue
		}

		// 4. Parse the json
		var auditData []auditItem
		err = json.Unmarshal(auditJSON, &auditData)
		if err != nil {
			// @TODO increment error counter
			log.Printf("Error unmarshaling JSON: %s\n", err.Error())
			time.Sleep(loopDelay)
			continue
		}

		// 5. At this point, none of the commands have failed, so we can call the heartbeat endpoint
		// @TODO call heartbeat endpoint

		newCount := uint64(len(auditData))
		log.Printf("Audit has %d items in the history\n", newCount)
		if newCount > currentCount {
			log.Printf("NEW COUNT (%d) IS GREATER THAN LAST KNOWN COUNT (%d)!!!\n", newCount, currentCount)

			// @TODO send alert(s)

			activities := ""
			for _, activity := range auditData[currentCount:] {
				marshalledParams, _ := json.Marshal(activity.Params)
				activities = fmt.Sprintf("%s - %s (%s)\n", activities, activity.Action, string(marshalledParams))
			}

			msg := map[string]string{
				"msg": fmt.Sprintf("%d new activities found on the pre-farm!\n%s\n", newCount - currentCount, activities),
			}

			body, _ := json.Marshal(msg)

			_, err = http.Post(testAlertURL, "application/json", bytes.NewBuffer(body))

			if err != nil {
				log.Printf("Error sending alert! %s\n", err.Error())
				return
			}
		} else if newCount < currentCount {
			// If the new count is less, something weird is going on, and we should alert because this is unexpected
			log.Printf("NEW COUNT (%d) IS LESS THAN THAN LAST KNOWN COUNT (%d)!!! THIS SHOULD NOT HAPPEN! COUNT SHOULD ONLY GO UP!\n", newCount, currentCount)
		}
		saveLastKnownCount(newCount)
		currentCount = newCount

		time.Sleep(loopDelay)
	}
}

func loadLastKnownCount() uint64 {
	if _, err := os.Stat(fileName); errors.Is(err, os.ErrNotExist) {
		return 0
	}

	bytes, err := os.ReadFile(fileName)
	if err != nil {
		log.Printf("Error reading last known audit count: %s\n", err.Error())
	}

	if len(bytes) == 0 {
		return 0
	}

	return BytesToUint64(bytes)
}

func saveLastKnownCount(count uint64) {
	err := os.WriteFile(fileName, Uint64ToBytes(count), 0644)
	if err != nil {
		log.Printf("Error writing last known file: %s\n", err.Error())
	}
}

// Uint64ToBytes Converts uint64 to []byte
func Uint64ToBytes(num uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, num)

	return b
}

// BytesToUint64 returns uint64 from []byte
// if you have more than eight bytes in your []byte this wont work like you think
func BytesToUint64(bytes []byte) uint64 {
	return binary.BigEndian.Uint64(bytes)
}
