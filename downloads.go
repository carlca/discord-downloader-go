package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/fatih/color"
	"github.com/hako/durafmt"
	"mvdan.cc/xurls/v2"
)

type download struct {
	URL         string
	Time        time.Time
	Destination string
	Filename    string
	ChannelID   string
	UserID      string
}

type downloadStatus int

//TODO: Make this not stupid
const (
	downloadSuccess downloadStatus = 0

	downloadSkippedDuplicate            downloadStatus = 1
	downloadSkippedUnpermittedDomain    downloadStatus = 2
	downloadSkippedUnpermittedType      downloadStatus = 3
	downloadSkippedUnpermittedExtension downloadStatus = 4

	downloadFailed                    downloadStatus = 5
	downloadFailedCreatingFolder      downloadStatus = 6
	downloadFailedRequesting          downloadStatus = 7
	downloadFailedDownloadingResponse downloadStatus = 8
	downloadFailedReadResponse        downloadStatus = 9
	downloadFailedCreatingSubfolder   downloadStatus = 10
	downloadFailedWritingFile         downloadStatus = 11
	downloadFailedWritingDatabase     downloadStatus = 12
)

type downloadStatusStruct struct {
	Status downloadStatus
	Error  error
}

func mDownloadStatus(status downloadStatus, _error ...error) downloadStatusStruct {
	if len(_error) == 0 {
		return downloadStatusStruct{
			Status: status,
			Error:  nil,
		}
	}
	return downloadStatusStruct{
		Status: status,
		Error:  _error[0],
	}
}

func getDownloadStatusString(status downloadStatus) string {
	switch status {
	case downloadSuccess:
		return "Download Succeeded"
	//
	case downloadSkippedDuplicate:
		return "Download Skipped - Duplicate"
	case downloadSkippedUnpermittedDomain:
		return "Download Skipped - Unpermitted Domain"
	case downloadSkippedUnpermittedType:
		return "Download Skipped - Unpermitted File Type"
	case downloadSkippedUnpermittedExtension:
		return "Download Skipped - Unpermitted File Extension"
	//
	case downloadFailed:
		return "Download Failed"
	case downloadFailedCreatingFolder:
		return "Download Failed - Error Creating Folder"
	case downloadFailedRequesting:
		return "Download Failed - Error Requesting URL Data"
	case downloadFailedDownloadingResponse:
		return "Download Failed - Error Downloading URL Response"
	case downloadFailedReadResponse:
		return "Download Failed - Error Reading URL Response"
	case downloadFailedCreatingSubfolder:
		return "Download Failed - Error Creating Subfolder for Type"
	case downloadFailedWritingFile:
		return "Download Failed - Error Writing File"
	case downloadFailedWritingDatabase:
		return "Download Failed - Error Writing to Database"
	}
	return "Unknown Error"
}

func isDiscordEmoji(link string) bool {
	// always match discord emoji URLs, eg https://cdn.discordapp.com/emojis/340989430460317707.png
	if strings.HasPrefix(link, "https://cdn.discordapp.com/emojis/") {
		return true
	}
	return false
}

// Trim duplicate links in link list
func trimDuplicateLinks(fileItems []*fileItem) []*fileItem {
	var result []*fileItem
	seen := map[string]bool{}

	for _, item := range fileItems {
		if seen[item.Link] {
			continue
		}

		seen[item.Link] = true
		result = append(result, item)
	}

	return result
}

func getRawLinks(m *discordgo.Message) []*fileItem {
	var links []*fileItem

	if m.Author == nil {
		m.Author = new(discordgo.User)
	}

	for _, attachment := range m.Attachments {
		links = append(links, &fileItem{
			Link:     attachment.URL,
			Filename: attachment.Filename,
		})
	}

	foundLinks := xurls.Strict().FindAllString(m.Content, -1)
	for _, foundLink := range foundLinks {
		links = append(links, &fileItem{
			Link: foundLink,
		})
	}

	for _, embed := range m.Embeds {
		if embed.URL != "" {
			links = append(links, &fileItem{
				Link: embed.URL,
			})
		}

		// Removing for now as this causes it to try and pull shit from things like YouTube descriptions
		/*if embed.Description != "" {
			foundLinks = xurls.Strict().FindAllString(embed.Description, -1)
			for _, foundLink := range foundLinks {
				links = append(links, &fileItem{
					Link: foundLink,
				})
			}
		}*/

		if embed.Image != nil && embed.Image.URL != "" {
			links = append(links, &fileItem{
				Link: embed.Image.URL,
			})
		}

		if embed.Video != nil && embed.Video.URL != "" {
			links = append(links, &fileItem{
				Link: embed.Video.URL,
			})
		}
	}

	return links
}

func getDownloadLinks(inputURL string, channelID string) map[string]string {
	logPrefixErrorHere := color.HiRedString("[getDownloadLinks]")

	/* TODO: Download Support...
	- TikTok: Tried, once the connection is closed the cdn URL is rendered invalid
	- Facebook Photos: Tried, it doesn't preload image data, it's loaded in after. Would have to keep connection open, find alternative way to grab, or use api.
	*/

	if regexUrlTwitter.MatchString(inputURL) {
		links, err := getTwitterUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Twitter Media fetch failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}
	if regexUrlTwitterStatus.MatchString(inputURL) {
		links, err := getTwitterStatusUrls(inputURL, channelID)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Twitter Status fetch failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}

	if regexUrlInstagram.MatchString(inputURL) {
		links, err := getInstagramUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Instagram fetch failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}

	if regexUrlFacebookVideo.MatchString(inputURL) || regexUrlFacebookVideoWatch.MatchString(inputURL) {
		links, err := getFacebookVideoUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Facebook Video fetch failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}

	if regexUrlImgurSingle.MatchString(inputURL) {
		links, err := getImgurSingleUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Imgur Media fetch failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}
	if regexUrlImgurAlbum.MatchString(inputURL) {
		links, err := getImgurAlbumUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Imgur Album fetch failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}

	if regexUrlStreamable.MatchString(inputURL) {
		links, err := getStreamableUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Streamable fetch failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}

	if regexUrlGfycat.MatchString(inputURL) {
		links, err := getGfycatUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Gfycat fetch failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}

	if regexUrlFlickrPhoto.MatchString(inputURL) {
		links, err := getFlickrPhotoUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Flickr Photo fetch failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}
	if regexUrlFlickrAlbum.MatchString(inputURL) {
		links, err := getFlickrAlbumUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Flickr Album fetch failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}
	if regexUrlFlickrAlbumShort.MatchString(inputURL) {
		links, err := getFlickrAlbumShortUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Flickr Album (short) fetch failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}
	if regexUrlGoogleDrive.MatchString(inputURL) {
		links, err := getGoogleDriveUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Google Drive Album URL for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}
	if regexUrlGoogleDriveFolder.MatchString(inputURL) {
		links, err := getGoogleDriveFolderUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Google Drive Folder URL for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}
	if regexUrlTistory.MatchString(inputURL) {
		links, err := getTistoryUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Tistory URL failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}
	if regexUrlTistoryLegacy.MatchString(inputURL) {
		links, err := getLegacyTistoryUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Legacy Tistory URL failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}
	// The original project has this as an option,
	if regexUrlPossibleTistorySite.MatchString(inputURL) {
		links, err := getPossibleTistorySiteUrls(inputURL)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Checking for Tistory site failed for %s -- %s", inputURL, err))
		} else if len(links) > 0 {
			return trimDownloadedLinks(links, channelID)
		}
	}

	if isDiscordEmoji(inputURL) {
		log.Println(logPrefixFileSkip, color.GreenString("Skipped %s as it is a Discord emoji", inputURL))
		return nil
	}

	// Try without queries
	parsedURL, err := url.Parse(inputURL)
	if err == nil {
		parsedURL.RawQuery = ""
		inputURLWithoutQueries := parsedURL.String()
		if inputURLWithoutQueries != inputURL {
			return trimDownloadedLinks(getDownloadLinks(inputURLWithoutQueries, channelID), channelID)
		}
	}

	return trimDownloadedLinks(map[string]string{inputURL: ""}, channelID)
}

func getFileLinks(m *discordgo.Message) []*fileItem {
	var fileItems []*fileItem

	linkTime, err := m.Timestamp.Parse()
	if err != nil {
		linkTime = time.Now()
	}

	rawLinks := getRawLinks(m)
	for _, rawLink := range rawLinks {
		downloadLinks := getDownloadLinks(
			rawLink.Link,
			m.ChannelID,
		)
		for link, filename := range downloadLinks {
			if rawLink.Filename != "" {
				filename = rawLink.Filename
			}

			fileItems = append(fileItems, &fileItem{
				Link:     link,
				Filename: filename,
				Time:     linkTime,
			})
		}
	}

	fileItems = trimDuplicateLinks(fileItems)

	return fileItems
}

func startDownload(inputURL string, filename string, path string, messageID string, channelID string, guildID string, userID string, fileTime time.Time, historyCmd bool) downloadStatusStruct {
	status := mDownloadStatus(downloadFailed)
	logPrefixErrorHere := color.HiRedString("[startDownload]")

	for i := 0; i < config.DownloadRetryMax; i++ {
		status = tryDownload(inputURL, filename, path, messageID, channelID, guildID, userID, fileTime, historyCmd)
		if status.Status < downloadFailed { // Success or Skip
			break
		} else {
			time.Sleep(5 * time.Second)
		}
	}

	if status.Status >= downloadFailed { // Any kind of failure
		log.Println(logPrefixErrorHere, color.RedString("Gave up on downloading %s", inputURL))
		if isChannelRegistered(channelID) {
			channelConfig := getChannelConfig(channelID)
			if !historyCmd && *channelConfig.ErrorMessages {
				content := fmt.Sprintf(
					"Gave up trying to download\n<%s>\nafter %d failed attempts...\n\n``%s``",
					inputURL, config.DownloadRetryMax, getDownloadStatusString(status.Status))
				if status.Error != nil {
					content = content + fmt.Sprintf("\n```ERROR: %s```", status.Error)
				}
				_, err := bot.ChannelMessageSendComplex(channelID,
					&discordgo.MessageSend{
						Content: fmt.Sprintf("<@!%s>", userID),
						Embed:   buildEmbed(channelID, "Download Failure", content),
					})
				if err != nil {
					log.Println(logPrefixErrorHere, color.HiRedString("Failed to send failure message to %s: %s", channelID, err))
				}
			}
		}
	}

	return status
}

func tryDownload(inputURL string, filename string, path string, messageID string, channelID string, guildID string, userID string, fileTime time.Time, historyCmd bool) downloadStatusStruct {
	cachedDownloadID++
	thisDownloadID := cachedDownloadID

	startTime := time.Now()

	logPrefixErrorHere := color.HiRedString("[tryDownload]")
	if isChannelRegistered(channelID) {
		channelConfig := getChannelConfig(channelID)

		// Clean/fix path
		if !strings.HasSuffix(path, "/") {
			path = path + "/"
		}

		// Create folder
		err := os.MkdirAll(path, 755)
		if err != nil {
			log.Println(logPrefixErrorHere, color.HiRedString("Error while creating destination folder \"%s\": %s", path, err))
			return mDownloadStatus(downloadFailedCreatingFolder, err)
		}

		// Request
		timeout := time.Duration(time.Duration(config.DownloadTimeout) * time.Second)
		client := &http.Client{
			Timeout: timeout,
		}
		request, err := http.NewRequest("GET", inputURL, nil)
		request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_13_4) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/66.0.3359.139 Safari/537.36")
		if err != nil {
			log.Println(logPrefixErrorHere, color.HiRedString("Error while requesting \"%s\": %s", inputURL, err))
			return mDownloadStatus(downloadFailedRequesting, err)
		}
		request.Header.Add("Accept-Encoding", "identity")
		response, err := client.Do(request)
		if err != nil {
			log.Println(logPrefixErrorHere, color.HiRedString("Error while receiving response from \"%s\": %s", inputURL, err))
			return mDownloadStatus(downloadFailedDownloadingResponse, err)
		}
		defer response.Body.Close()

		// Download duration
		if config.DebugOutput {
			log.Println(logPrefixDebug, color.YellowString("#%d - %s to download.", thisDownloadID, durafmt.ParseShort(time.Since(startTime)).String()))
		}
		downloadTime := time.Now()

		// Filename
		if filename == "" {
			filename = filenameFromURL(response.Request.URL.String())
			for key, iHeader := range response.Header {
				if key == "Content-Disposition" {
					_, params, err := mime.ParseMediaType(iHeader[0])
					if err == nil {
						newFilename, err := url.QueryUnescape(params["filename"])
						if err != nil {
							newFilename = params["filename"]
						}
						if newFilename != "" {
							filename = newFilename
						}
					}
				}
			}
		}

		// Read
		bodyOfResp, err := ioutil.ReadAll(response.Body)
		if err != nil {
			log.Println(logPrefixErrorHere, color.HiRedString("Could not read response from \"%s\": %s", inputURL, err))
			return mDownloadStatus(downloadFailedReadResponse, err)
		}

		contentType := http.DetectContentType(bodyOfResp)
		contentTypeParts := strings.Split(contentType, "/")
		contentTypeFound := contentTypeParts[0]

		// Check for valid filename, if not, replace with generic filename
		if !regexFilename.MatchString(filename) {
			filename = "InvalidFilename"
			possibleExtension, _ := mime.ExtensionsByType(contentType)
			if len(possibleExtension) > 0 {
				filename += possibleExtension[0]
			}
		}

		// Check Domain
		if channelConfig.DomainBlacklist != nil {
			u, err := url.Parse(inputURL)
			if err != nil {
				log.Println(logPrefixErrorHere, color.RedString("Error while parsing url for DomainBlacklist:\t%s", err))
			} else if stringInSlice(u.Hostname(), *channelConfig.DomainBlacklist) {
				log.Println(logPrefixFileSkip, color.GreenString("Unpermitted domain (%s) found at %s", u.Hostname(), inputURL))
				return mDownloadStatus(downloadSkippedUnpermittedDomain)
			}
		}

		// Check content type
		if !((*channelConfig.SaveImages && contentTypeFound == "image") ||
			(*channelConfig.SaveVideos && contentTypeFound == "video") ||
			(*channelConfig.SaveAudioFiles && contentTypeFound == "audio") ||
			(*channelConfig.SaveTextFiles && contentTypeFound == "text") ||
			(*channelConfig.SaveOtherFiles && contentTypeFound == "application")) {
			log.Println(logPrefixFileSkip, color.GreenString("Unpermitted filetype (%s) found at %s", contentTypeFound, inputURL))
			return mDownloadStatus(downloadSkippedUnpermittedType)
		}

		// Check extension
		extension := filepath.Ext(filename)
		if stringInSlice(extension, *channelConfig.ExtensionBlacklist) || stringInSlice(extension, []string{".com", ".net", ".org"}) {
			log.Println(logPrefixFileSkip, color.GreenString("Unpermitted extension (%s) found at %s", extension, inputURL))
			return mDownloadStatus(downloadSkippedUnpermittedExtension)
		}

		// Subfolder division
		subfolder := ""
		if *channelConfig.DivideFoldersByType {
			switch contentTypeFound {
			case "image":
				subfolder = "images/"
			case "video":
				subfolder = "videos/"
			case "audio":
				subfolder = "audio/"
			case "text":
				subfolder = "text/"
			case "application":
				if stringInSlice(extension, []string{".mov"}) {
					contentTypeFound = "video"
					subfolder = "videos/"
				} else if stringInSlice(extension, []string{".psd"}) ||
					stringInSlice(extension, []string{".nef"}) ||
					stringInSlice(extension, []string{".dng"}) ||
					stringInSlice(extension, []string{".tif"}) ||
					stringInSlice(extension, []string{".tiff"}) {
					contentTypeFound = "image"
					subfolder = "images/"
				} else {
					subfolder = "applications/"
				}
			}
			if subfolder != "" {
				// Create folder.
				err := os.MkdirAll(path+subfolder, 755)
				if err != nil {
					log.Println(logPrefixErrorHere, color.HiRedString("Error while creating subfolder \"%s\": %s", path, err))
					return mDownloadStatus(downloadFailedCreatingSubfolder, err)
				}
			}
		}

		// Format filename/path
		filenameDateFormat := config.FilenameDateFormat
		if channelConfig.OverwriteFilenameDateFormat != nil {
			if *channelConfig.OverwriteFilenameDateFormat != "" {
				filenameDateFormat = *channelConfig.OverwriteFilenameDateFormat
			}
		}
		newFilename := time.Now().Format(filenameDateFormat) + filename
		//completePath := path + string(os.PathSeparator) + subfolder + newFilename
		completePath := path + subfolder + newFilename

		// Check if exists
		if _, err := os.Stat(completePath); err == nil {
			if *channelConfig.SavePossibleDuplicates {
				tmpPath := completePath
				i := 1
				for {
					// Append number to name
					completePath = tmpPath[0:len(tmpPath)-len(filepathExtension(tmpPath))] +
						"-" + strconv.Itoa(i) + filepathExtension(tmpPath)
					if _, err := os.Stat(completePath); os.IsNotExist(err) {
						break
					}
					i = i + 1
				}
				log.Println(color.GreenString("Matching filenames, possible duplicate? Saving \"%s\" as \"%s\" instead", tmpPath, completePath))
			} else {
				log.Println(logPrefixFileSkip, color.GreenString("Matching filenames, possible duplicate..."))
				return mDownloadStatus(downloadSkippedDuplicate)
			}
		}

		// Write
		err = ioutil.WriteFile(completePath, bodyOfResp, 0644)
		if err != nil {
			log.Println(logPrefixErrorHere, color.HiRedString("Error while writing file to disk \"%s\": %s", inputURL, err))
			return mDownloadStatus(downloadFailedWritingFile, err)
		}

		// Change file time
		err = os.Chtimes(completePath, fileTime, fileTime)
		if err != nil {
			log.Println(logPrefixErrorHere, color.RedString("Error while changing metadata date \"%s\": %s", inputURL, err))
		}

		// Write duration
		if config.DebugOutput {
			log.Println(logPrefixDebug, color.YellowString("#%d - %s to save.", thisDownloadID, durafmt.ParseShort(time.Since(downloadTime)).String()))
		}
		writeTime := time.Now()

		// Output
		sourceChannelName := channelID
		sourceGuildName := "N/A"
		sourceChannel, err := bot.State.Channel(channelID)
		if err != nil {
			log.Println(logPrefixErrorHere, color.HiRedString("Error fetching channel state for %s: %s", channelID, err))
		}
		if sourceChannel != nil && sourceChannel.Name != "" {
			sourceChannelName = sourceChannel.Name
			sourceGuild, _ := bot.State.Guild(sourceChannel.GuildID)
			if sourceGuild != nil && sourceGuild.Name != "" {
				sourceGuildName = sourceGuild.Name
			}
		}
		log.Println(color.HiGreenString("SAVED FILE (%s) sent in \"%s\"#%s to \"%s\"", contentTypeFound, sourceGuildName, sourceChannelName, completePath))

		// Store in db
		err = dbInsertDownload(&download{
			URL:         inputURL,
			Time:        time.Now(),
			Destination: completePath,
			Filename:    filename,
			ChannelID:   channelID,
			UserID:      userID,
		})
		if err != nil {
			log.Println(logPrefixErrorHere, color.HiRedString("Error writing to database: %s", err))
			return mDownloadStatus(downloadFailedWritingDatabase, err)
		}

		// Storage & output duration
		if config.DebugOutput {
			log.Println(logPrefixDebug, color.YellowString("#%d - %s to update database.", thisDownloadID, durafmt.ParseShort(time.Since(writeTime)).String()))
		}
		finishTime := time.Now()

		// React
		if !historyCmd && *channelConfig.ReactWhenDownloaded {
			reaction := ""
			if *channelConfig.ReactWhenDownloadedEmoji == "" {
				guild, err := bot.State.Guild(guildID)
				if err != nil {
					log.Println(logPrefixErrorHere, color.RedString("Error fetching guild state for emojis from %s: %s", guildID, err))
				} else {
					emojis := guild.Emojis
					if len(emojis) > 1 {
						for {
							rand.Seed(time.Now().UnixNano())
							chosenEmoji := emojis[rand.Intn(len(emojis))]
							formattedEmoji := chosenEmoji.APIName()
							if !chosenEmoji.Animated && !stringInSlice(formattedEmoji, *channelConfig.BlacklistReactEmojis) {
								reaction = formattedEmoji
								break
							}
						}
					} else {
						reaction = "✅"
					}
				}
			} else {
				reaction = *channelConfig.ReactWhenDownloadedEmoji
			}
			bot.MessageReactionAdd(channelID, messageID, reaction)
			// React duration
			if config.DebugOutput {
				log.Println(logPrefixDebug, color.YellowString("#%d - %s to react with \"%s\".", thisDownloadID, durafmt.ParseShort(time.Since(finishTime)).String(), reaction))
			}
			finishTime = time.Now()
		}

		timeLastUpdated = time.Now()
		if *channelConfig.UpdatePresence {
			updateDiscordPresence()
		}

		if config.DebugOutput {
			log.Println(logPrefixDebug, color.YellowString("#%d - %s total.", thisDownloadID, time.Since(startTime)))
		}

		return mDownloadStatus(downloadSuccess)
	}
	return mDownloadStatus(downloadFailed)
}
