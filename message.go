package xmpp

import (
	"fmt"
	"github.com/mattn/go-xmpp"
	"github.com/oklahomer/go-sarah"
	"strings"
	"time"
)

// NewMessageInput creates and returns MessageInput instance that represents incoming chat message.
func NewMessageInput(message *xmpp.Chat, receivedAt time.Time) *MessageInput {
	// Since XMPP is a real-time based protocol, sending timestamp is not part of the core spec.
	// However, with XEP-0203, a timestamp can be set for delayed message.
	// https://xmpp.org/extensions/xep-0203.html
	//
	// To satisfy sarah.Input interface, this adapter uses message reception time as message sending time
	// when no timestamp is available.
	t := message.Stamp
	if t.IsZero() {
		t = receivedAt
	}
	return &MessageInput{
		Event:     message,
		timestamp: t,
	}
}

// MessageInput represents sarah.Input implementation that represents incoming chat message.
// Developers may directly refer to its field, Event, to access additional detailed data.
type MessageInput struct {
	Event     *xmpp.Chat
	timestamp time.Time
}

var _ sarah.Input = (*MessageInput)(nil)

// SenderKey returns a text representing the message sender.
// Returned value is equivalent to the JID specified in "from" attribute.
// https://tools.ietf.org/html/rfc3920#section-9.1.2
func (message *MessageInput) SenderKey() string {
	return fmt.Sprintf("%s", trimResource(message.Event.Remote))
}

// Message returns message content.
func (message *MessageInput) Message() string {
	return message.Event.Text
}

// SentAt returns the timestamp when the message is sent.
// Since XMPP is a real-time based protocol, sending timestamp is not part of the core spec.
// This returns delayed timestamp derived from XEP-0203 when possible; otherwise returns the reception time.
func (message *MessageInput) SentAt() time.Time {
	return message.timestamp
}

// ReplyTo returns replying destination.
// Currently its value is a plain text representing JID of the sender.
// This may be a "full JID" that contains "/resource" part when the message type is "groupchat."
// https://xmpp.org/extensions/xep-0045.html#terms
func (message *MessageInput) ReplyTo() sarah.OutputDestination {
	return message.Event.Remote
}

func trimResource(jid string) string {
	return strings.Split(jid, "/")[0]
}
