/*
Copyright © 2022 Denis Halturin <dhalturin@hotmail.com>
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice,
   this list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its contributors
   may be used to endorse or promote products derived from this software
   without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE
LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
POSSIBILITY OF SUCH DAMAGE.
*/
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/opsgenie/opsgenie-go-sdk-v2/alert"
	"github.com/opsgenie/opsgenie-go-sdk-v2/schedule"
	log "github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	configFile          = ""
	configPath          = ""
	logFormat           = ""
	logFormatJsonPretty = false
	logLevel            = ""
	rootCmd             = &cobra.Command{
		Use:               pkg,
		Short:             "Utility for integrating on-duty Opsgenie and Slack",
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		Version:           version,
	}
	pkg     = "opsgin"
	version = ""
)

type Schedule struct {
	duty  []string
	group string
	name  string
	token []string
}

type Schedules struct {
	groups map[string]string
	list   []Schedule
	mode   string

	// opsgenie clients
	ac *alert.Client
	sc *schedule.Client

	// slack clients
	slack *slack.Client
	sm    *socketmode.Client

	log *log.Entry
}

func (s *Schedules) configGetSchedules() error {
	for item := range viper.AllSettings() {
		r, _ := regexp.Compile(`^_`)
		if r.MatchString(item) {
			continue
		}

		data := viper.GetStringSlice(item)

		schedule := Schedule{
			group: item,
			name:  data[0],
		}

		switch s.mode {
		case "daemon":
			schedule.token = data[1:]
		case "sync":
			schedule.duty = data[1:]
		default:
			return fmt.Errorf("unknown app mode")
		}

		s.list = append(s.list, schedule)
	}

	return nil
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	ex, err := os.Executable()
	if err != nil {
		log.WithField("err", err).Fatal("failed get path name")
	}

	ExecutableDir := filepath.Dir(ex)

	pathConf, _ := filepath.Abs(ExecutableDir + "/../../../etc/" + pkg)

	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&configPath, "config-path", pathConf, "Set the configuration file path")
	rootCmd.PersistentFlags().StringVar(&configFile, "config-file", "config.yaml", "Set the configuration file name")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "Set the log format: text, json")
	rootCmd.PersistentFlags().BoolVar(&logFormatJsonPretty, "log-pretty", false, "Json logs will be indented")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Set the log level: debug, info, warn, error, fatal")
}

func initConfig() {
	viper.AutomaticEnv()
	viper.SetConfigFile(fmt.Sprintf("%s/%s", configPath, configFile))
	viper.SetDefault("_opsgenie.messages.alert_acknowledged.failure", "Failed to update alert status :sob:")
	viper.SetDefault("_opsgenie.messages.alert_acknowledged.success", "The engineer on duty has read the notification (_user_)")
	viper.SetDefault("_opsgenie.messages.alert_close.failure", ":bangbang: Failed to close alert")
	viper.SetDefault("_opsgenie.messages.alert_close.success", ":dizzy: The alert was closed")
	viper.SetDefault("_opsgenie.messages.alert_create.failure", "I couldn't create an alert in OpsGenie :sob:")
	viper.SetDefault("_opsgenie.messages.alert_create.success", "The engineer on duty has been notified and will be coming soon")
	viper.SetDefault("_opsgenie.messages.alert_increase_priority.failure", ":bangbang: Failed to increase alert priority")
	viper.SetDefault("_opsgenie.messages.alert_increase_priority.success", ":fire: The alert priority has been increased")
	viper.SetDefault("_opsgenie.messages.alert_increase_priority.tip", ":no_entry_sign: You can increase the priority of the notification, but be careful not to do this if it is not necessary")
	viper.SetDefault("_opsgenie.messages.command.help", "Available arguments for slash commands: *who*, *w*")
	viper.SetDefault("_opsgenie.messages.command.on_duty", "The engineer on duty - _user_")
	viper.SetDefault("_opsgenie.messages.command.unknown", ":bangbang: Unknown command")
	viper.SetDefault("_opsgenie.messages.fields.on_duty", "On duty")
	viper.SetDefault("_opsgenie.messages.fields.priority", "Priority")
	viper.SetDefault("_opsgenie.messages.fields.priority_p1_after", "P1 after _time_")
	viper.SetDefault("_opsgenie.priority", "P5")
	viper.SetDefault("_opsgenie.priority_auto_increase", "90")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.SetEnvPrefix(pkg)

	level, err := log.ParseLevel(logLevel)
	if err != nil {
		log.WithField("err", err).Fatal("can't parse log level")
	}

	log.SetLevel(level)
	log.SetFormatter(
		&log.TextFormatter{
			ForceColors:     true,
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339,
		},
	)

	if logFormat == "json" {
		log.SetFormatter(&log.JSONFormatter{
			PrettyPrint: logFormatJsonPretty,
		})
	}

	if err := viper.ReadInConfig(); err == nil {
		log.Debugf("Using config file: %s", viper.ConfigFileUsed())
	} else {
		log.Error(err)
	}
}
