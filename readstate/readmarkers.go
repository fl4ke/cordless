package readstate

import (
	"strconv"
	"sync"
	"time"

	"github.com/Bios-Marcel/cordless/discordutil"
	"github.com/Bios-Marcel/discordgo"
)

var (
	data       = make(map[string]uint64)
	timerMutex = &sync.Mutex{}
	ackTimers  = make(map[string]*time.Timer)
	state      *discordgo.State
)

// Load loads the locally saved readmarkers returing an error if this failed.
func Load(sessionState *discordgo.State) {
	for _, channelState := range sessionState.ReadState {
		lastMessageID := channelState.GetLastMessageID()
		if lastMessageID == "" {
			continue
		}

		parsed, parseError := strconv.ParseUint(lastMessageID, 10, 64)
		if parseError != nil {
			continue
		}

		data[channelState.ID] = parsed
	}

	state = sessionState
}

// ClearReadStateFor clears all entries for the given Channel.
func ClearReadStateFor(channelID string) {
	timerMutex.Lock()
	delete(data, channelID)
	delete(ackTimers, channelID)
	timerMutex.Unlock()
}

// UpdateReadLocal can be used to locally update the data without sending
// anything to the Discord API. The update will only be applied if the new
// message ID is greater than the old one.
func UpdateReadLocal(channelID string, lastMessageID string) bool {
	parsed, parseError := strconv.ParseUint(lastMessageID, 10, 64)
	if parseError != nil {
		return false
	}

	old, isPresent := data[channelID]
	if !isPresent || old < parsed {
		data[channelID] = parsed
		return true
	}

	return false
}

// UpdateRead tells the discord server that a channel has been read. If the
// channel has already been read and this method was called needlessly, then
// this will be a No-OP.
func UpdateRead(session *discordgo.Session, channel *discordgo.Channel, lastMessageID string) error {
	// Avoid unnecessary traffic
	if HasBeenRead(channel, lastMessageID) {
		return nil
	}

	parsed, parseError := strconv.ParseUint(lastMessageID, 10, 64)
	if parseError != nil {
		return parseError
	}

	data[channel.ID] = parsed

	_, ackError := session.ChannelMessageAck(channel.ID, lastMessageID, "")
	return ackError
}

// UpdateReadBuffered triggers an acknowledgement after a certain amount of
// seconds. If this message is called again during that time, the timer will
// be reset. This avoid unnecessarily many calls to the Discord servers.
func UpdateReadBuffered(session *discordgo.Session, channel *discordgo.Channel, lastMessageID string) {
	timerMutex.Lock()
	ackTimer := ackTimers[channel.ID]
	if ackTimer == nil {
		newTimer := time.NewTimer(4 * time.Second)
		ackTimers[channel.ID] = newTimer
		go func() {
			<-newTimer.C
			ackTimers[channel.ID] = nil
			UpdateRead(session, channel, lastMessageID)
		}()
	} else {
		ackTimer.Reset(4 * time.Second)
	}
	timerMutex.Unlock()
}

// IsGuildMuted returns whether the user muted the given guild.
func IsGuildMuted(guildID string) bool {
	for _, settings := range state.UserGuildSettings {
		if settings.GuildID == guildID {
			if settings.Muted {
				return true
			}

			break
		}
	}

	return false
}

// HasGuildBeenRead returns true if the guild has no unread messages or is
// muted.
func HasGuildBeenRead(guildID string) bool {
	if IsGuildMuted(guildID) {
		return true
	}

	realGuild, cacheError := state.Guild(guildID)
	if cacheError == nil {
		for _, channel := range realGuild.Channels {
			if !discordutil.HasReadMessagesPermission(channel.ID, state) {
				continue
			}

			if !HasBeenRead(channel, channel.LastMessageID) {
				return false
			}
		}
	}

	return true
}

// HasBeenRead checks whether the passed channel has an unread Message or not.
func HasBeenRead(channel *discordgo.Channel, lastMessageID string) bool {
	if lastMessageID == "" {
		return true
	}

	if channel.GuildID != "" {
		for _, settings := range state.UserGuildSettings {
			if settings.GuildID == channel.GuildID {
				for _, override := range settings.ChannelOverrides {
					if override.ChannelID == channel.ID {
						if override.Muted {
							return true
						}
					}
				}

				break
			}
		}
	}

	data, present := data[channel.ID]
	if !present {
		return false
	}

	parsed, parseError := strconv.ParseUint(lastMessageID, 10, 64)
	if parseError != nil {
		return true
	}

	return data >= parsed
}
