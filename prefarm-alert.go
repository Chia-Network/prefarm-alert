package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
)

const (
	fileName = ".audit-count"
)

type auditItem struct {
	Time   int64       `json:"time"` // Unix Timestamp
	Action string      `json:"action"`
	Params auditParams `json:"params"`
}

type auditParams struct {
	FromRoot          string  `json:"from_root"`
	ToRoot            string  `json:"to_root"`
	Completed         *bool   `json:"completed,omitempty"`
	CompletedAtHeight *uint64 `json:"completed_at_height,omitempty"`
}

func main() {
	currentCount := loadLastKnownCount()

	// 1. Get a new temp file name
	file, err := ioutil.TempFile("", "cic-audit")
	if err != nil {
		log.Fatal(err)
	}
	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			log.Printf("Error cleeaning up temp file %s: %s\n", file.Name(), err.Error())
		}
	}(file.Name())

	// 2. Call the sync command, and check for any errors
	syncCmd := exec.Command("/Users/chrismarslender/Projects/internal-custody/venv/bin/cic", "sync")
	syncCmd.Dir = "/Users/chrismarslender/Projects/internal-custody"
	syncCmd.Env = append(syncCmd.Env, "PATH=/Users/chrismarslender/Projects/internal-custody/venv/bin/")
	_, err = syncCmd.Output()

	if err != nil {
		log.Fatalf("Error running sync command: %s\n", err.Error())
		return
	}

	// 3. Call the audit command and have it write to the temp file
	auditCmd := exec.Command("/Users/chrismarslender/Projects/internal-custody/venv/bin/cic", "audit", "-f", file.Name())
	auditCmd.Dir = "/Users/chrismarslender/Projects/internal-custody"
	auditCmd.Env = append(auditCmd.Env, "PATH=/Users/chrismarslender/Projects/internal-custody/venv/bin/")
	_, err = auditCmd.Output()

	if err != nil {
		log.Fatalf("Error running audit command: %s\n", err.Error())
		return
	}

	// 4. Read the json from the temp file
	auditJSON, err := os.ReadFile(file.Name())
	if err != nil {
		log.Fatalf("Error reading audit json file: %s\n", err.Error())
	}

	var auditData []auditItem
	err = json.Unmarshal(auditJSON, &auditData)
	if err != nil {
		log.Fatalf("Error unmarshaling JSON: %s\n", err.Error())
	}

	// 5. At this point, none of the commands have failed, so we can call the heartbeat endpoint
	// @TODO call heartbeat endpoint

	newCount := uint64(len(auditData))
	log.Printf("Audit has %d items in the history\n", newCount)
	if newCount > currentCount {
		log.Printf("NEW COUNT (%d) IS GREATER THAN LAST KNOWN COUNT (%d)!!!\n", newCount, currentCount)
		// @TODO send alert(s)
	}
	saveLastKnownCount(newCount)
	currentCount = newCount
}

func loadLastKnownCount() uint64 {
	if _, err := os.Stat(fileName); errors.Is(err, os.ErrNotExist) {
		return 0
	}

	bytes, err := os.ReadFile(fileName)
	if err != nil {
		log.Fatalf("Error reading last known audit count: %s\n", err.Error())
	}

	if len(bytes) == 0 {
		return 0
	}

	return BytesToUint64(bytes)
}

func saveLastKnownCount(count uint64) {
	err := os.WriteFile(fileName, Uint64ToBytes(count), 0644)
	if err != nil {
		log.Fatalf("Error writing last known file: %s\n", err.Error())
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
