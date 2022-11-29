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
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/spf13/viper"
)

func (s *Schedules) slackInit() error {
	if s.slack != nil {
		return nil
	}

	var (
		slack_app_key string // xapp
		slack_key     string // xoxp or xoxb
		slack_type    string
	)

	switch s.mode {
	case "daemon":
		for _, item := range s.list[0].token {
			if ok, _ := regexp.MatchString(`xox[p,b]-`, item); ok {
				slack_key = item
			}

			if strings.Contains(item, "xapp-") {
				slack_app_key = item
			}
		}

		slack_type = "appname"
	case "sync":
		slack_key = viper.GetString("slack.api.key")
		slack_type = "usergroup"
	default:
		return fmt.Errorf("unknown app mode")
	}

	s.log = log.WithFields(log.Fields{
		slack_type: s.list[0].group,
		"schedule": s.list[0].name,
	})

	s.log.Info("init slack client")

	s.slack = slack.New(
		slack_key,
		slack.OptionAppLevelToken(slack_app_key),
		// slack.OptionDebug(true),
	)

	return nil
}

func (s *Schedules) slackClientsWS() error {
	for _, item := range s.list {
		schedule := Schedules{
			list: []Schedule{item},
			mode: s.mode,
		}

		if err := schedule.slackConnectToWS(); err != nil {
			return err
		}
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigs

	log.WithFields(log.Fields{
		"signal": sig.String(),
		"code":   fmt.Sprintf("%d", sig),
	}).Info("Signal notify")

	return nil
}

func (s *Schedules) slackConnectToWS() error {
	if err := s.slackInit(); err != nil {
		return err
	}

	if _, err := s.slack.AuthTest(); err != nil {
		return err
	}

	s.sm = socketmode.New(
		s.slack,
		// socketmode.OptionDebug(true),
	)

	go s.slackWatchEvents()

	go s.sm.Run()

	return nil
}

func (s *Schedules) slackGetAttachmentAction(action ...string) []slack.AttachmentAction {
	actionList := []slack.AttachmentAction{}

	for _, item := range action {
		switch item {
		case "alert_increase_priority":
			actionList = append(actionList, slack.AttachmentAction{
				Confirm: &slack.ConfirmationField{
					Text: viper.GetString("_opsgenie.messages.alert_increase_priority.tip"),
				},
				Name:  "alert_increase_priority",
				Style: "danger",
				Text:  "Increase priority",
				Type:  "button",
				Value: "alert_increase_priority",
			})
		case "alert_acknowledge":
			actionList = append(actionList, slack.AttachmentAction{
				Name:  "alert_acknowledge",
				Text:  "Ack",
				Type:  "button",
				Value: "alert_acknowledge",
			})
		case "alert_close":
			actionList = append(actionList, slack.AttachmentAction{
				Name:  "alert_close",
				Style: "primary",
				Text:  "Close",
				Type:  "button",
				Value: "alert_close",
			})
		default:
			continue
		}
	}

	return actionList
}

func (s *Schedules) slackGetAttachmentFields(priority, duty string, priorityAutoIncrease int) []slack.AttachmentField {
	attachmentField := []slack.AttachmentField{
		{Short: true, Title: viper.GetString("_opsgenie.messages.fields.priority"), Value: priority},
		{Short: true, Title: viper.GetString("_opsgenie.messages.fields.on_duty"), Value: fmt.Sprintf("<@%s>", duty)},
	}

	if priorityAutoIncrease > 0 {
		attachmentField = append(
			attachmentField,
			slack.AttachmentField{
				Short: true,
				Title: strings.Replace(
					viper.GetString("_opsgenie.messages.fields.priority_p1_after"),
					"_time_",
					fmt.Sprintf("%02d:%02d", priorityAutoIncrease/60, priorityAutoIncrease%60),
					-1,
				),
			},
		)
	}

	return attachmentField
}

func (s *Schedules) slackWatchEvents() {
	priority_auto_increase, err := strconv.Atoi(viper.GetString("_opsgenie.priority_auto_increase"))
	if err != nil {
		s.log.Fatal(err)
	}

	priority_auto_increase_alert := make(map[string]bool)

	for envelope := range s.sm.Events {
		switch envelope.Type {
		case
			socketmode.EventTypeEventsAPI,
			socketmode.EventTypeInteractive,
			socketmode.EventTypeSlashCommand:
			s.log.Debugf("event type: %v", envelope.Type)
		default:
			s.log.Debugf("skipped: %v", envelope.Type)
			continue
		}

		s.sm.Ack(*envelope.Request)

		// s.log.Debugf(">> envelope - %#v\n", envelope)

		s.log.Debugf("getting schedule - %#v", s.list[0].name)
		if err := s.opsgenieGetSchedules(s.list[0].name); err != nil {
			s.log.Errorf("can't load schedule - %s", err.Error())

			continue
		}

		s.log.Debug("getting slack users")
		if err := s.slackFindUsers(); err != nil {
			s.log.Errorf("can't load slack users - %s", err.Error())

			continue
		}

		slackAttachmentAction := s.slackGetAttachmentAction("alert_increase_priority", "alert_acknowledge", "alert_close")
		slackAttachmentField := s.slackGetAttachmentFields(viper.GetString("_opsgenie.priority"), s.list[0].duty[0], priority_auto_increase)
		slackAttachmentColor := "warning"
		slackResponse := viper.GetString("_opsgenie.messages.alert_create.success")

		switch envelope.Type {
		case socketmode.EventTypeInteractive:
			payload, _ := envelope.Data.(slack.InteractionCallback)
			data := strings.Split(payload.CallbackID, ";")

			slackAttachmentField = s.slackGetAttachmentFields(data[1], s.list[0].duty[0], 0)

			delete(priority_auto_increase_alert, data[0])

			switch payload.ActionCallback.AttachmentActions[0].Value {
			case "alert_close":
				slackAttachmentColor = "good"
				slackResponse = viper.GetString("_opsgenie.messages.alert_close.success")

				if err := s.opsgenieCloseAlert(data[0]); err != nil {
					slackResponse = viper.GetString("_opsgenie.messages.alert_close.failure")

					s.log.Errorf("can't close alert - %s", err.Error())
				} else {
					slackAttachmentAction = []slack.AttachmentAction{}
				}
			case "alert_acknowledge":
				slackAttachmentColor = "#039be5"
				slackResponse = viper.GetString("_opsgenie.messages.alert_acknowledged.success")

				if err := s.opsgenieAckAlert(data[0]); err != nil {
					slackResponse = viper.GetString("_opsgenie.messages.alert_acknowledged.failure")

					s.log.Errorf("can't ack alert - %s", err.Error())
				} else {
					slackAttachmentAction = s.slackGetAttachmentAction("alert_close")
				}
			case "alert_increase_priority":
				data[1] = "P1"

				slackAttachmentField = s.slackGetAttachmentFields(data[1], s.list[0].duty[0], 0)
				slackAttachmentColor = "danger"
				slackResponse = viper.GetString("_opsgenie.messages.alert_increase_priority.success")

				if err := s.opsgenieIncreaseAlertPriority(data[0], data[1]); err != nil {
					slackResponse = viper.GetString("_opsgenie.messages.alert_increase_priority.failure")

					s.log.Errorf("can't close alert - %s", err.Error())
				} else {
					slackAttachmentAction = s.slackGetAttachmentAction("alert_acknowledge", "alert_close")
				}
			default:
				continue
			}

			if _, _, err := s.slack.PostMessage(
				payload.Channel.GroupConversation.Conversation.ID,
				slack.MsgOptionReplaceOriginal(payload.ResponseURL),
				slack.MsgOptionAttachments(slack.Attachment{
					Actions:    slackAttachmentAction,
					CallbackID: strings.Join(data, ";"),
					Color:      slackAttachmentColor,
					Fields:     slackAttachmentField,
					Text:       strings.Replace(slackResponse, "_user_", fmt.Sprintf("<@%s>", payload.User.ID), -1),
				}),
			); err != nil {
				s.log.Error(err)
			}

		case socketmode.EventTypeEventsAPI:
			payload, _ := envelope.Data.(slackevents.EventsAPIEvent)

			switch event := payload.InnerEvent.Data.(type) {
			case *slackevents.AppMentionEvent:
				var msg slackevents.MessageEvent

				json.Unmarshal([]byte(*payload.Data.(*slackevents.EventsAPICallbackEvent).InnerEvent), &msg)

				if msg.Edited != nil {
					continue
				}

				s.log.Debug("getting permalink")
				link, err := s.slack.GetPermalink(&slack.PermalinkParameters{
					Channel: event.Channel,
					Ts:      event.TimeStamp,
				})
				if err != nil {
					s.log.Errorf("can't get permalink - %s", err.Error())

					continue
				}

				ts := event.TimeStamp
				if event.ThreadTimeStamp != "" {
					ts = event.ThreadTimeStamp
				}

				s.log.Debug("adding opsgenie alert")
				alertID, err := s.opsgenieAddAlert(event.Text, ts, link)
				if err != nil {
					slackAttachmentAction = []slack.AttachmentAction{}
					slackAttachmentField = []slack.AttachmentField{}
					slackResponse = viper.GetString("_opsgenie.messages.alert_create.failure")

					s.log.Errorf("can't create alert - %s", err.Error())
				}

				s.log.Debugf("sending slack.PostMessage to %s", event.User)
				_, respTS, err := s.slack.PostMessage(
					event.Channel,
					slack.MsgOptionTS(event.TimeStamp),
					slack.MsgOptionAttachments(slack.Attachment{
						Actions:    slackAttachmentAction,
						CallbackID: fmt.Sprintf("%s;%s", alertID, viper.GetString("_opsgenie.priority")),
						Color:      slackAttachmentColor,
						Fields:     slackAttachmentField,
						Text:       strings.Replace(slackResponse, "_user_", fmt.Sprintf("<@%s>", event.User), -1),
					}),
				)
				if err != nil {
					s.log.Error(err)
				}

				go func() {
					duty := s.list[0].duty[0]
					priority := viper.GetString("_opsgenie.priority")
					priority_auto_increase_alert[alertID] = true

					for curTime := priority_auto_increase; curTime >= 0; curTime-- {
						if !priority_auto_increase_alert[alertID] {
							return
						}

						if curTime == 0 {
							slackAttachmentColor = "danger"
							priority = "P1"

							if err := s.opsgenieIncreaseAlertPriority(alertID, priority); err != nil {
								slackResponse = viper.GetString("_opsgenie.messages.alert_increase_priority.failure")

								s.log.Errorf("can't close alert - %s", err.Error())
							} else {
								slackAttachmentAction = s.slackGetAttachmentAction("alert_acknowledge", "alert_close")
							}
						}

						slackAttachmentField = s.slackGetAttachmentFields(priority, duty, curTime)

						if curTime%5 == 0 {
							_, _, _, err := s.slack.UpdateMessage(
								event.Channel,
								respTS,
								slack.MsgOptionAttachments(slack.Attachment{
									Actions:    slackAttachmentAction,
									CallbackID: fmt.Sprintf("%s;%s", alertID, priority),
									Color:      slackAttachmentColor,
									Fields:     slackAttachmentField,
									Text:       slackResponse,
								}),
							)
							if err != nil {
								s.log.Error(err)
							}
						}

						time.Sleep(1 * time.Second)
					}
				}()
			}

		case socketmode.EventTypeSlashCommand:
			payload, _ := envelope.Data.(slack.SlashCommand)

			switch payload.Text {
			case "w", "who":
				slackResponse = viper.GetString("_opsgenie.messages.command.on_duty")
			case "":
				slackResponse = viper.GetString("_opsgenie.messages.command.help")
			default:
				slackResponse = viper.GetString("_opsgenie.messages.command.unknown")
			}

			if _, err := s.slack.PostEphemeral(
				payload.ChannelID,
				payload.UserID,
				slack.MsgOptionText(
					strings.Replace(slackResponse, "_user_", fmt.Sprintf("<@%s>", s.list[0].duty[0]), -1),
					false,
				),
			); err != nil {
				s.log.Error(err)
			}
		}
	}
}

func (s *Schedules) slackUpdateUserGroup() error {
	if err := s.slackInit(); err != nil {
		return err
	}

	if err := s.slackGetUserGroups(); err != nil {
		log.Fatal(err)
	}

	if err := s.slackFindUsers(); err != nil {
		log.Fatal(err)
	}

	for _, item := range s.list {
		if item.group == "" {
			continue
		}

		duty := []string{}

		for _, uid := range item.duty {
			if uid == "" {
				continue
			}

			duty = append(duty, uid)
		}

		if len(duty) < 1 {
			s.log.Warn("there are no on-duty on this calendar")

			continue
		}

		if _, err := s.slack.UpdateUserGroupMembers(item.group, strings.Join(duty, ",")); err != nil {
			s.log.Error(err)

			continue
		}

		s.log.Infof("the user group has been updated")
	}

	return nil
}

func (s *Schedules) slackGetUserGroups() error {
	if err := s.slackInit(); err != nil {
		return err
	}

	s.groups = make(map[string]string)

	groups, err := s.slack.GetUserGroups()
	if err != nil {
		return err
	}

	for _, group := range groups {
		s.groups[group.Handle] = group.ID
	}

	s.log.Debugf("slack user groups: %#v", s.groups)

	for idx, item := range s.list {
		if len(item.duty) < 1 {
			continue
		}

		group, ok := s.groups[item.group]
		if !ok {
			s.log.Errorf("can't find group id")
		}

		s.list[idx].group = group
	}

	return nil
}

func (s *Schedules) slackFindUsers() error {
	if err := s.slackInit(); err != nil {
		return err
	}

	for _, item := range s.list {
		if len(item.duty) < 1 {
			continue
		}

		for idx, duty := range item.duty {
			user, err := s.slack.GetUserByEmail(duty)
			if err != nil {
				s.log.Warnf("can't find user %#v", duty)

				user = &slack.User{} // the user will be removed from duty
			}

			item.duty[idx] = user.ID
		}
	}

	return nil
}
