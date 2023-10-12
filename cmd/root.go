package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/go-yaml/yaml"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/chia-network/go-chia-libs/pkg/rpc"
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
		heartbeatURL := viper.GetString("heartbeat-url")
		singletonName := viper.GetString("name")

		initJSONFile()

		client, err := rpc.NewClient(rpc.ConnectionModeHTTP, rpc.WithAutoConfig(), rpc.WithBaseURL(&url.URL{
			Scheme: "https",
			Host:   viper.GetString("chia-hostname"),
		}))
		if err != nil {
			log.Fatalf("Error starting RPC Client: %s\n", err.Error())
		}

		for {
			// First, ensure the full node is actually synced
			// If it's not synced, we should just abort now, so we trigger heartbeat alerts
			state, _, err := client.FullNodeService.GetBlockchainState()
			if err != nil {
				// If this happens over and over, eventually the uptime robot heartbeat will fail
				// at that point, we'll check why this is failing, so no need to keep track of repeated errors here
				log.Printf("Error getting blockchain state: %s\n", err.Error())
				time.Sleep(loopDelay)
				continue
			}

			if !state.BlockchainState.IsAbsent() {
				log.Println("Unexpected response from RPC")
				time.Sleep(loopDelay)
				continue
			}

			if !state.BlockchainState.MustGet().Sync.Synced {
				// If this happens over and over, eventually the uptime robot heartbeat will fail
				// at that point, we'll check why this is failing, so no need to keep track of repeated errors here
				log.Println("Full node is not synced. Can't continue until the node is synced.")
				time.Sleep(loopDelay)
				continue
			}

			// Call the sync command, and check for any errors
			syncCmd := exec.Command(fmt.Sprintf("%s/bin/cic", venvpath), "sync", "-c", observerData)
			syncCmd.Dir = datadir
			syncCmd.Env = append(syncCmd.Env, fmt.Sprintf("PATH=%s/bin/", venvpath))
			if chiaRootSet {
				syncCmd.Env = append(syncCmd.Env, fmt.Sprintf("CHIA_ROOT=%s", chiaRoot))
			}
			_, err = syncCmd.Output()

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
					activities = fmt.Sprintf("%s**(%s) %s**\nSingleton: %s\n%s\n\n", activities, eventType, activity.New.Action, singletonName, string(marshalledYaml))
				}

				msg := map[string]string{
					"msg": fmt.Sprintf(":warning: %d new activities found on a tracked custody singleton! :warning:\n%s\n", diffCount, activities),
				}

				body, _ := json.Marshal(msg)

				_, err = http.Post(alertURL, "application/json", bytes.NewBuffer(body))

				if err != nil {
					log.Printf("Error sending alert! %s\n", err.Error())
					continue
				}
			}

			// At this point, none of the commands have failed, so we can call the heartbeat endpoint
			if heartbeatURL != "" {
				_, err := http.Get(heartbeatURL)
				if err != nil {
					log.Printf("Error calling heartbeat endpoint: %s\n", err.Error())
					continue
				}
			}

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

		// singletonName is a friendly name to refer to singletons by, since we could be tracking many
		singletonName string

		chiaHostname string
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
	rootCmd.PersistentFlags().StringVar(&singletonName, "name", "", "A friendly name to refer to this singleton, used in alerts")
	rootCmd.PersistentFlags().StringVar(&chiaHostname, "chia-hostname", "localhost", "The hostname to use to connect to Chia RPC")

	cobra.CheckErr(viper.BindPFlag("venv-path", rootCmd.PersistentFlags().Lookup("venv-path")))
	cobra.CheckErr(viper.BindPFlag("data-dir", rootCmd.PersistentFlags().Lookup("data-dir")))
	cobra.CheckErr(viper.BindPFlag("observer-data", rootCmd.PersistentFlags().Lookup("observer-data")))
	cobra.CheckErr(viper.BindPFlag("json-filename", rootCmd.PersistentFlags().Lookup("json-filename")))
	cobra.CheckErr(viper.BindPFlag("loop-delay", rootCmd.PersistentFlags().Lookup("loop-delay")))
	cobra.CheckErr(viper.BindPFlag("heartbeat-url", rootCmd.PersistentFlags().Lookup("heartbeat-url")))
	cobra.CheckErr(viper.BindPFlag("alert-url", rootCmd.PersistentFlags().Lookup("alert-url")))
	cobra.CheckErr(viper.BindPFlag("name", rootCmd.PersistentFlags().Lookup("name")))
	cobra.CheckErr(viper.BindPFlag("chia-hostname", rootCmd.PersistentFlags().Lookup("chia-hostname")))
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

// If the json file doesn't exist, we just write an empty list to it so audit --diff doesn't error
func initJSONFile() {
	filepath := getJSONFilePath()
	if _, err := os.Stat(filepath); errors.Is(err, os.ErrNotExist) {
		err = os.WriteFile(filepath, []byte("[]"), 0644)
		if err != nil {
			log.Fatalf("Error initializing json data file. Err: %s\n", err.Error())
		}
	}
}

func getJSONFilePath() string {
	dir := viper.GetString("data-dir")
	file := viper.GetString("json-filename")

	return path.Join(dir, file)
}
