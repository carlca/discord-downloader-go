package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/fatih/color"
	"github.com/hako/durafmt"
	"mvdan.cc/xurls/v2"
)

type FileItem struct {
	Link     string
	Filename string
	Time     time.Time
}

var (
	skipCommands = []string{
		"skip",
		"ignore",
		"don't save",
		"no save",
	}
)

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if (m.Author.ID == user.ID && !config.ScanOwnMessages) || !isChannelRegistered(m.ChannelID) {
		return
	}

	channelConfig := getChannelConfig(m.ChannelID)
	if !*channelConfig.Enabled {
		return
	}

	var sendLabel string
	if config.DebugOutput {
		sendLabel = fmt.Sprintf("%s/%s/%s", m.GuildID, m.ChannelID, m.Author.ID)
	} else {
		sendLabel = fmt.Sprintf("%s in \"%s\"#%s", getUserIdentifier(*m.Author), getGuildName(m.GuildID), getChannelName(m.ChannelID))
	}

	content := m.Content
	if len(m.Attachments) > 0 {
		content = content + fmt.Sprintf(" (%d attachments)", len(m.Attachments))
	}
	log.Println(color.CyanString("Message [%s]: %s", sendLabel, content))

	canSkip := config.AllowSkipping
	if channelConfig.OverwriteAllowSkipping != nil {
		canSkip = *channelConfig.OverwriteAllowSkipping
	}
	if canSkip {
		for _, cmd := range skipCommands {
			if strings.Contains(m.Content, cmd) {
				log.Println(color.HiYellowString("Message handling skipped due to use of skip command."))
				return
			}
		}
	}

	handleMessage(m.Message)
}

func messageUpdate(s *discordgo.Session, m *discordgo.MessageUpdate) {
	if m.EditedTimestamp != discordgo.Timestamp("") {
		if (m.Author.ID == user.ID && !config.ScanOwnMessages) || !isChannelRegistered(m.ChannelID) {
			return
		}

		channelConfig := getChannelConfig(m.ChannelID)
		if !*channelConfig.Enabled || !*channelConfig.ScanEdits {
			return
		}

		var sendLabel string
		if config.DebugOutput {
			sendLabel = fmt.Sprintf("%s/%s/%s", m.GuildID, m.ChannelID, m.Author.ID)
		} else {
			sendLabel = fmt.Sprintf("%s in \"%s\"#%s", getUserIdentifier(*m.Author), getGuildName(m.GuildID), getChannelName(m.ChannelID))
		}

		content := m.Content
		if len(m.Attachments) > 0 {
			content = content + fmt.Sprintf(" (%d attachments)", len(m.Attachments))
		}
		log.Println(color.CyanString("Edited Message [%s]: %s", sendLabel, content))

		canSkip := config.AllowSkipping
		if channelConfig.OverwriteAllowSkipping != nil {
			canSkip = *channelConfig.OverwriteAllowSkipping
		}
		if canSkip {
			for _, cmd := range skipCommands {
				if m.Content == cmd {
					log.Println(color.HiYellowString("Message handling skipped due to use of skip command."))
					return
				}
			}
		}

		handleMessage(m.Message)
	}
}

func handleMessage(m *discordgo.Message) {
	if isChannelRegistered(m.ChannelID) {
		channelConfig := getChannelConfig(m.ChannelID)
		files := getFileLinks(m)
		for _, file := range files {
			log.Println(color.CyanString("> FILE: " + file.Link))

			startDownload(
				file.Link,
				file.Filename,
				channelConfig.Destination,
				m.ID,
				m.ChannelID,
				m.GuildID,
				m.Author.ID,
				file.Time,
				false,
			)
		}
	}
}

var (
	historyCommandActive map[string]string
)

func handleHistory(commandingMessage *discordgo.Message, commandingChannelID string, subjectChannelID string) int {
	historyCommandActive[subjectChannelID] = "downloading"

	i := 0

	if isChannelRegistered(subjectChannelID) {
		channelConfig := getChannelConfig(subjectChannelID)

		historyStartTime := time.Now()

		message, err := replyEmbed(commandingMessage, "Command — History", "Starting to catalog channel history, please wait...")
		if err != nil {
			log.Println(color.HiRedString("[handleHistory] Failed to send command embed message (requested by %s):\t%s", getUserIdentifier(*commandingMessage.Author), err))
		}
		log.Println(color.HiCyanString("[handleHistory] %s began cataloging history for %s", getUserIdentifier(*commandingMessage.Author), subjectChannelID))

		lastBefore := ""
		var lastBeforeTime time.Time
	MessageRequestingLoop:
		for true {
			if lastBeforeTime != (time.Time{}) {
				log.Println(color.CyanString("[handleHistory] Requesting 100 more messages, %d cataloged, (before %s)",
					i, lastBeforeTime))
				// Status update
				content := fmt.Sprintf("``%s:`` %d files cataloged\n_Requesting more messages, please wait..._",
					durafmt.ParseShort(time.Since(historyStartTime)).String(), i)
				message, err = bot.ChannelMessageEditComplex(&discordgo.MessageEdit{
					ID:      message.ID,
					Channel: message.ChannelID,
					Embed:   buildEmbed(message.ChannelID, "Command — History", content),
				})
				// Edit failure
				if err != nil {
					log.Println(color.RedString("[handleHistory] Failed to edit status message, sending new one:\t%s", err))
					message, err = replyEmbed(message, "Command — History", content)
					if err != nil {
						log.Println(color.HiRedString("[handleHistory] Failed to send replacement status message:\t%s", err))
					}
				}
			}
			messages, err := bot.ChannelMessages(subjectChannelID, 100, lastBefore, "", "")
			if err == nil {
				if len(messages) <= 0 {
					delete(historyCommandActive, subjectChannelID)
					break MessageRequestingLoop
				}
				lastBefore = messages[len(messages)-1].ID
				lastBeforeTime, err = messages[len(messages)-1].Timestamp.Parse()
				if err != nil {
					log.Println(color.RedString("[handleHistory] Failed to fetch message timestamp:\t%s", err))
				}
				for _, message := range messages {
					fileTime := time.Now()
					if message.Timestamp != "" {
						fileTime, err = message.Timestamp.Parse()
						if err != nil {
							log.Println(color.RedString("[handleHistory] Failed to parse message timestamp:\t%s", err))
						}
					}
					if historyCommandActive[message.ChannelID] == "cancel" {
						delete(historyCommandActive, message.ChannelID)
						break MessageRequestingLoop
					}
					for _, iAttachment := range message.Attachments {
						if len(dbFindDownloadByURL(iAttachment.URL)) == 0 {
							download := startDownload(
								iAttachment.URL,
								iAttachment.Filename,
								channelConfig.Destination,
								message.ID,
								message.ChannelID,
								message.GuildID,
								message.Author.ID,
								fileTime,
								true,
							)
							if download.Status == DownloadSuccess {
								i++
							}
						}
					}
					foundUrls := xurls.Strict().FindAllString(message.Content, -1)
					for _, iFoundUrl := range foundUrls {
						links := getDownloadLinks(iFoundUrl, subjectChannelID)
						for link, filename := range links {
							if len(dbFindDownloadByURL(link)) == 0 {
								download := startDownload(
									link,
									filename,
									channelConfig.Destination,
									message.ID,
									message.ChannelID,
									message.GuildID,
									message.Author.ID,
									fileTime,
									true,
								)
								if download.Status == DownloadSuccess {
									i++
								}
							}
						}
					}
				}
			} else {
				// Error requesting messages
				_, err = replyEmbed(message, "Command — History", fmt.Sprintf("Encountered an error requesting messages: %s", err.Error()))
				if err != nil {
					log.Println(color.HiRedString("[handleHistory] Failed to send error message:\t%s", err))
				}
				log.Println(color.HiRedString("[handleHistory] Error requesting messages:\t%s", err))
				delete(historyCommandActive, subjectChannelID)
				break MessageRequestingLoop
			}
		}
		// Final status update
		contentFinal := fmt.Sprintf("``%s:`` **%s total files saved!**\n\nFinished cataloging history for ``%s``\n\n_Duration was %s_",
			durafmt.ParseShort(time.Since(historyStartTime)).String(),
			formatNumber(int64(i)),
			commandingMessage.ChannelID,
			durafmt.Parse(time.Since(historyStartTime)).String(),
		)
		message, err = bot.ChannelMessageEditComplex(&discordgo.MessageEdit{
			ID:      message.ID,
			Channel: message.ChannelID,
			Embed:   buildEmbed(message.ID, "Command — History", contentFinal),
		})
		// Edit failure
		if err != nil {
			log.Println(color.RedString("[handleHistory] Failed to edit status message, sending new one:\t%s", err))
			message, err = replyEmbed(message, "Command — History", contentFinal)
			if err != nil {
				log.Println(color.HiRedString("[handleHistory] Failed to send replacement status message:\t%s", err))
			}
		}
		// Final log
		log.Println(color.HiCyanString("[handleHistory] Finished cataloging history for %s... started downloading %d files... (requested by %s)",
			commandingMessage.ChannelID, i, getUserIdentifier(*commandingMessage.Author)),
		)
	}

	return i
}