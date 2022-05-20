package cmd

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
	"path"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type auditItem struct {
	Time   int64           `json:"time"` // Unix Timestamp
	Action string          `json:"action"`
	Params json.RawMessage `json:"params"`
}

var cfgFile string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "prefarm-alert",
	Short: "Alerts when changes are happening to a custody singleton",
	Run: func(cmd *cobra.Command, args []string) {
		currentCount := loadLastKnownCount()

		// Check if CHIA_ROOT is set
		chiaRoot, chiaRootSet := os.LookupEnv("CHIA_ROOT")
		venvpath := viper.GetString("venv-path")
		datadir := viper.GetString("data-dir")
		datafile := viper.GetString("data-file")
		loopDelay := viper.GetDuration("loop-delay") * time.Second
		alertURL := viper.GetString("alert-url")

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
				// If this happens over and over, eventually the uptime robot heartbeat will fail
				// at that point, we'll check why this is failing, so no need to keep track of repeated errors here
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
				// If this happens over and over, eventually the uptime robot heartbeat will fail
				// at that point, we'll check why this is failing, so no need to keep track of repeated errors here
				log.Printf("Error running audit command: %s\n", err.Error())
				time.Sleep(loopDelay)
				continue
			}

			// 4. Parse the json
			var auditData []auditItem
			err = json.Unmarshal(auditJSON, &auditData)
			if err != nil {
				// If this happens over and over, eventually the uptime robot heartbeat will fail
				// at that point, we'll check why this is failing, so no need to keep track of repeated errors here
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
					"msg": fmt.Sprintf("%d new activities found on the pre-farm!\n%s\n", newCount-currentCount, activities),
				}

				body, _ := json.Marshal(msg)

				_, err = http.Post(alertURL, "application/json", bytes.NewBuffer(body))

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
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	var (
		// icVenvPath is the path to the internal custody venv
		icVenvPath string

		// dataDir is the directory that will contain all data related to this particular singleton
		// last json, configuration txt file, etc
		dataDir string

		// dataFile is the file within dataDir that has the data about the singleton we're tracking
		dataFile string

		// lastJSONFileName is the filename to keep the last received json inside (used to diff for changes)
		lastJSONFileName string

		// loopDelaySeconds is how many seconds to delay between each sync/audit
		// This needs to be multiplied by time.Seconds to actually get to seconds
		loopDelay time.Duration

		// heartbeatURL the URL to ping every time we have a successful loop without any unexpected errors
		heartbeatURL string

		// alertWebhookURL is the URL to send alerts to when changes are detected on the singleton
		alertWebhookURL string
	)

	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.prefarm-alert.yaml)")

	rootCmd.PersistentFlags().StringVar(&icVenvPath, "venv-path", "/internal-custody/venv", "The path to the internal custody venv")
	rootCmd.PersistentFlags().StringVar(&dataDir, "data-dir", "/data", "The directory that contains all data related to a single singleton to be tracked")
	rootCmd.PersistentFlags().StringVar(&dataFile, "data-file", "Observer Info.txt", "The file that contains the info about the singleton. Should be relative to datadir.")
	rootCmd.PersistentFlags().StringVar(&lastJSONFileName, "json-filename", ".audit-json", "Keeps the last received json so the results can be diffed each iteration")
	rootCmd.PersistentFlags().DurationVar(&loopDelay, "loop-delay", 30, "How many seconds in between each audit check")
	rootCmd.PersistentFlags().StringVar(&heartbeatURL, "heartbeat-url", "", "The URL to send heartbeat events to when a loop completes with no errors")
	rootCmd.PersistentFlags().StringVar(&alertWebhookURL, "alert-url", "", "The URL to send webhook alerts to when things change in the singleton")

	checkErr(viper.BindPFlag("venv-path", rootCmd.PersistentFlags().Lookup("venv-path")))
	checkErr(viper.BindPFlag("data-dir", rootCmd.PersistentFlags().Lookup("data-dir")))
	checkErr(viper.BindPFlag("data-file", rootCmd.PersistentFlags().Lookup("data-file")))
	checkErr(viper.BindPFlag("json-filename", rootCmd.PersistentFlags().Lookup("json-filename")))
	checkErr(viper.BindPFlag("loop-delay", rootCmd.PersistentFlags().Lookup("loop-delay")))
	checkErr(viper.BindPFlag("heartbeat-url", rootCmd.PersistentFlags().Lookup("heartbeat-url")))
	checkErr(viper.BindPFlag("alert-url", rootCmd.PersistentFlags().Lookup("alert-url")))
}

func checkErr(err error) {
	if err != nil {
		log.Fatalln(err.Error())
	}
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".prefarm-alert" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".prefarm-alert")
	}

	viper.SetEnvPrefix("PREFARM_ALERT")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}

func getJSONFilePath() string {
	dir := viper.GetString("data-dir")
	file := viper.GetString("json-filename")

	return path.Join(dir, file)
}

func loadLastKnownCount() uint64 {
	if _, err := os.Stat(getJSONFilePath()); errors.Is(err, os.ErrNotExist) {
		return 0
	}

	bytes, err := os.ReadFile(getJSONFilePath())
	if err != nil {
		log.Printf("Error reading last known audit count: %s\n", err.Error())
	}

	if len(bytes) == 0 {
		return 0
	}

	return BytesToUint64(bytes)
}

func saveLastKnownCount(count uint64) {
	err := os.WriteFile(getJSONFilePath(), Uint64ToBytes(count), 0644)
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
