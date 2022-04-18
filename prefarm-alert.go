package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
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

	// 2. Call the audit command and have it write to the temp file
	cmd := exec.Command("/Users/chrismarslender/Projects/internal-custody/venv/bin/cic", "audit", "-f", file.Name())
	cmd.Dir = "/Users/chrismarslender/Projects/internal-custody"
	cmd.Env = append(cmd.Env, "PATH=/Users/chrismarslender/Projects/internal-custody/venv/bin/")
	_, err = cmd.Output()

	if err != nil {
		log.Fatalf("Error running audit command: %s\n", err.Error())
		return
	}

	// 3 Read the json from the temp file
	auditJSON, err := os.ReadFile(file.Name())
	if err != nil {
		log.Fatalf("Error reading audit json file: %s\n", err.Error())
	}

	var auditData []auditItem
	err = json.Unmarshal(auditJSON, &auditData)
	if err != nil {
		log.Fatalf("Error unmarshaling JSON: %s\n", err.Error())
	}

	log.Printf("Audit has %d items in the history\n", len(auditData))
}
