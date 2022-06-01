package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/go-yaml/yaml"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type auditDiff struct {
	Old *auditItem `json:"old,omitempty"`
	New *auditItem `json:"new,omitempty"`
}

type auditItem struct {
	Time   int64                  `json:"time"` // Unix Timestamp
	Action string                 `json:"action"`
	Params map[string]interface{} `json:"params"`
}

var cfgFile string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "prefarm-alert",
	Short: "Alerts when changes are happening to a custody singleton",
	Run: func(cmd *cobra.Command, args []string) {
		// Check if CHIA_ROOT is set
		chiaRoot, chiaRootSet := os.LookupEnv("CHIA_ROOT")
		venvpath := viper.GetString("venv-path")
		datadir := viper.GetString("data-dir")
		observerData := viper.GetString("observer-data")
		loopDelay := viper.GetDuration("loop-delay") * time.Second
		alertURL := viper.GetString("alert-url")

		for {
			// Call the sync command, and check for any errors
			syncCmd := exec.Command(fmt.Sprintf("%s/bin/cic", venvpath), "sync", "-c", observerData)
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

			// Call the audit command to get the diff between previous json and current audit events
			auditDiffCmd := exec.Command(fmt.Sprintf("%s/bin/cic", venvpath), "audit", "-d", getJSONFilePath())
			auditDiffCmd.Dir = datadir
			auditDiffCmd.Env = append(syncCmd.Env, fmt.Sprintf("PATH=%s/bin/", venvpath))
			if chiaRootSet {
				auditDiffCmd.Env = append(syncCmd.Env, fmt.Sprintf("CHIA_ROOT=%s", chiaRoot))
			}
			auditDiffJSON, err := auditDiffCmd.Output()
			if err != nil {
				// If this happens over and over, eventually the uptime robot heartbeat will fail
				// at that point, we'll check why this is failing, so no need to keep track of repeated errors here
				log.Printf("Error running audit diff command: %s\n", err.Error())
				time.Sleep(loopDelay)
				continue
			}

			// Call the audit command to save the raw audit JSON, after we successfully got the diff output
			auditCmd := exec.Command(fmt.Sprintf("%s/bin/cic", venvpath), "audit", "-f", getJSONFilePath())
			auditCmd.Dir = datadir
			auditCmd.Env = append(syncCmd.Env, fmt.Sprintf("PATH=%s/bin/", venvpath))
			if chiaRootSet {
				auditCmd.Env = append(syncCmd.Env, fmt.Sprintf("CHIA_ROOT=%s", chiaRoot))
			}
			_, err = auditCmd.Output()
			if err != nil {
				// If this happens over and over, eventually the uptime robot heartbeat will fail
				// at that point, we'll check why this is failing, so no need to keep track of repeated errors here
				log.Printf("Error running audit command: %s\n", err.Error())
				time.Sleep(loopDelay)
				continue
			}

			// Parse the json diff
			var auditDiffResult []auditDiff
			err = json.Unmarshal(auditDiffJSON, &auditDiffResult)
			if err != nil {
				// If this happens over and over, eventually the uptime robot heartbeat will fail
				// at that point, we'll check why this is failing, so no need to keep track of repeated errors here
				log.Printf("Error unmarshaling audit diff JSON: %s\n", err.Error())
				time.Sleep(loopDelay)
				continue
			}

			diffCount := len(auditDiffResult)

			log.Printf("Audit diff found %d new events!\n", diffCount)
			if diffCount > 0 {
				activities := ""
				for _, activity := range auditDiffResult {
					marshalledYaml, _ := yaml.Marshal(activity.New.Params)
					var eventType string
					if activity.Old != nil {
						eventType = "UPDATED EVENT"
					} else {
						eventType = "NEW EVENT"
					}
					activities = fmt.Sprintf("%s**(%s) %s**\n%s\n\n", activities, eventType, activity.New.Action, string(marshalledYaml))
				}

				msg := map[string]string{
					"msg": fmt.Sprintf(":rotating_light: :rotating_light: %d new activities found on the pre-farm! :rotating_light: :rotating_light:\n%s\n", diffCount, activities),
				}

				body, _ := json.Marshal(msg)

				_, err = http.Post(alertURL, "application/json", bytes.NewBuffer(body))

				if err != nil {
					log.Printf("Error sending alert! %s\n", err.Error())
					continue
				}
			}

			// At this point, none of the commands have failed, so we can call the heartbeat endpoint
			// @TODO call heartbeat endpoint

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

		// observerData is the file within dataDir that has the data about the singleton we're tracking
		observerData string

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
	rootCmd.PersistentFlags().StringVar(&observerData, "observer-data", "/observer-info.txt", "The file that contains the info about the singleton. Should be an absolute path.")
	rootCmd.PersistentFlags().StringVar(&lastJSONFileName, "json-filename", ".audit-json", "Keeps the last received json so the results can be diffed each iteration")
	rootCmd.PersistentFlags().DurationVar(&loopDelay, "loop-delay", 30, "How many seconds in between each audit check")
	rootCmd.PersistentFlags().StringVar(&heartbeatURL, "heartbeat-url", "", "The URL to send heartbeat events to when a loop completes with no errors")
	rootCmd.PersistentFlags().StringVar(&alertWebhookURL, "alert-url", "", "The URL to send webhook alerts to when things change in the singleton")

	checkErr(viper.BindPFlag("venv-path", rootCmd.PersistentFlags().Lookup("venv-path")))
	checkErr(viper.BindPFlag("data-dir", rootCmd.PersistentFlags().Lookup("data-dir")))
	checkErr(viper.BindPFlag("observer-data", rootCmd.PersistentFlags().Lookup("observer-data")))
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
