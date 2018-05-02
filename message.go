package xmpp

import (
	"fmt"
	"time"

	"github.com/mattn/go-xmpp"
	"github.com/oklahomer/go-sarah"
)

// NewMessageInput creates and returns MessageInput instance.
func NewMessageInput(message xmpp.Chat) *MessageInput {
	return &MessageInput{
		event: &message,
	}
}

// MessageInput satisfies Input interface for an xmpp.Chat message
type MessageInput struct {
	event *xmpp.Chat
}

// SenderKey returns string representing message sender.
func (message *MessageInput) SenderKey() string {
	// not sure this is right - for slack the first param is channelID
	// I would have thought room name
	return fmt.Sprintf("%s|%s", message.SenderKey(), message.event.Remote)
}

// Message returns body message.
func (message *MessageInput) Message() string {
	return message.event.Text
}

// SentAt returns message event's timestamp.
func (message *MessageInput) SentAt() time.Time {
	return message.event.Stamp
}

// ReplyTo returns slack channel to send reply to.
// TODO - maybe for groupchat we need to set this to message.Replyto ??
// or does remote contain room name (check)
func (message *MessageInput) ReplyTo() sarah.OutputDestination {
	return message.event.Remote
}
