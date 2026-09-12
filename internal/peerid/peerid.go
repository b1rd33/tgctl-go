// Package peerid keeps Telegram's overlapping peer namespaces distinct using
// marked IDs: users positive, basic groups negative, channels below -10^12.
package peerid

const ChannelOffset int64 = 1_000_000_000_000

func User(raw int64) int64    { return raw }
func Chat(raw int64) int64    { return -raw }
func Channel(raw int64) int64 { return -ChannelOffset - raw }
func Raw(marked int64) int64 {
	if marked < -ChannelOffset {
		return -marked - ChannelOffset
	}
	if marked < 0 {
		return -marked
	}
	return marked
}
func Kind(marked int64) string {
	if marked < -ChannelOffset {
		return "channel"
	}
	if marked < 0 {
		return "chat"
	}
	if marked > 0 {
		return "user"
	}
	return ""
}
