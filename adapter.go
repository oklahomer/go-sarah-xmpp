package xmpp

import (
	"crypto/tls"
	"strings"
	"time"

	"github.com/mattn/go-xmpp"
	"github.com/oklahomer/go-sarah"
	"github.com/oklahomer/go-sarah/log"

	"fmt"
	"golang.org/x/net/context"
)

const (
	XMPP sarah.BotType = "xmpp"
)

// AdapterOption defines function signature that Adapter's
// functional option must satisfy.
type AdapterOption func(adapter *Adapter) error

// Adapter offers Bot developers easy way to communicate with Xmpp.
// This implements sarah.Adapter interface, so this instance can be fed
// to sarah.Runner as below.
type Adapter struct {
	config        *Config
	stanzaHandler StanzaHandler
	messageQueue  chan xmpp.Chat
}

type StanzaHandler func(ctx context.Context, config *Config, stanza DecodedPayload, enqueueInput func(sarah.Input) error)

func defaultStanzaHandler(_ context.Context, config *Config, stanza DecodedPayload, enqueueInput func(input sarah.Input) error) {
	switch typedStanza := stanza.(type) {
	case xmpp.Chat:
		if parseNick(typedStanza.Remote) == parseNick(config.Jid) {
			// Skip message from this bot.
			return
		}

		if !typedStanza.Stamp.IsZero() {
			// Skip delayed messages.
			// https://xmpp.org/extensions/xep-0203.html
			return
		}

		input := NewMessageInput(&typedStanza, time.Now())

		trimmed := strings.TrimSpace(input.Message())
		if config.HelpCommand != "" && trimmed == config.HelpCommand {
			// Help command
			help := sarah.NewHelpInput(input.SenderKey(), input.Message(), input.SentAt(), input.ReplyTo())
			enqueueInput(help)
		} else if config.AbortCommand != "" && trimmed == config.AbortCommand {
			// Abort command
			abort := sarah.NewAbortInput(input.SenderKey(), input.Message(), input.SentAt(), input.ReplyTo())
			enqueueInput(abort)
		} else {
			// Regular input
			enqueueInput(input)
		}

	case xmpp.Presence:
		// do nothing

	}
}

func WithStanzaHandler(handler StanzaHandler) AdapterOption {
	return func(adapter *Adapter) error {
		adapter.stanzaHandler = handler
		return nil
	}
}

// NewAdapter creates new Adapter with given *Config and zero or more AdapterOption.
func NewAdapter(config *Config, options ...AdapterOption) (*Adapter, error) {
	var err error
	adapter := &Adapter{
		config:        config,
		stanzaHandler: defaultStanzaHandler,
		messageQueue:  make(chan xmpp.Chat, 100), // TODO customizable queue size
	}

	for _, opt := range options {
		err = opt(adapter)
		if err != nil {
			return nil, err
		}
	}

	return adapter, nil
}

// BotType returns BotType of this particular instance.
func (adapter *Adapter) BotType() sarah.BotType {
	return XMPP
}

// Set up and watch the xmpp connection
func (adapter *Adapter) Run(ctx context.Context, enqueueInput func(sarah.Input) error, notifyErr func(error)) {
	for {
		client, err := adapter.createXMPP()
		if err != nil {
			notifyErr(sarah.NewBotNonContinuableError(err.Error()))
		}

		// Create connection specific context so each connection-scoped goroutine can receive connection closing event and eventually return.
		connCtx, connCancel := context.WithCancel(ctx)

		// This channel is not subject to close. This channel can be accessed in parallel manner with nonBlockSignal(),
		// and the receiver is NOT looking for close signal. Let GC run when this channel is no longer referred.
		//
		// http://stackoverflow.com/a/8593986
		// "Note that it is only necessary to close a channel if the receiver is looking for a close.
		// Closing the channel is a control signal on the channel indicating that no more data follows."
		tryPing := make(chan struct{}, 1)

		go adapter.receiveStanza(connCtx, client, tryPing, enqueueInput)

		connErr := adapter.superviseConnection(connCtx, client, tryPing)
		// superviseConnection returns when parent context is canceled or connection is hopelessly unstable.
		// Close current connection and do some cleanup.
		client.Close() // Make sure to close the connection.
		connCancel()   // Cancel all ongoing tasks for current connection to avoid goroutine leaks.

		if connErr == nil {
			// Connection is intentionally closed by caller.
			// No more interaction follows.
			return
		}

		log.Errorf("Will try re-connecting due to previous connection's fatal state: %s.", connErr.Error())
	}
}

func (adapter *Adapter) createXMPP() (*xmpp.Client, error) {
	tc := new(tls.Config)
	tc.InsecureSkipVerify = adapter.config.SkipTLSVerify
	tc.ServerName = strings.Split(adapter.config.Server, ":")[0]
	options := xmpp.Options{
		Host:                         adapter.config.Server,
		User:                         adapter.config.Jid,
		Password:                     adapter.config.Password,
		NoTLS:                        true,
		StartTLS:                     true,
		TLSConfig:                    tc,
		Debug:                        adapter.config.Debug,
		Session:                      true,
		Status:                       "",
		StatusMessage:                "",
		Resource:                     "",
		InsecureAllowUnencryptedAuth: false,
	}

	return options.NewClient()
}

// Listens to current XMPP connection and receive incoming XMPP stanzas.
// On successful stanza reception, this passes received stanza to StanzaHandler.
// Developer may override default StanzaHandler by passing StanzaHandler implementation to NewAdapter() as below:
//
// 	myHandler := func(ctx context.Context, config *Config, stanza DecodedPayload, enqueueInput func(sarah.Input) error) {
//		// Do something
//	}
//	opt := WithStanzaHandler(myHandler)
//	adapter, err := NewAdapter(config, opt)
//
// TODO Instead of receiving *xmpp.Client, let this method receive an interface that has Recv() method for easier unit testing
func (adapter *Adapter) receiveStanza(ctx context.Context, client *xmpp.Client, tryPing chan<- struct{}, enqueueInput func(sarah.Input) error) {
	for {
		select {
		case <-ctx.Done():
			log.Info("Stop receiving payload due to context cancel")
			return

		default:
			stanza, err := client.Recv()
			if err != nil {
				// Failed to receive stanza. Try ping to check connection status.
				select {
				case tryPing <- struct{}{}:
					// O.K
					continue

				default:
					// couldn't send because no goroutine is receiving channel or is busy.
					log.Infof("Not sending ping signal because another goroutine already sent one and is being processed."+
						"The cause at this moment: %s", err.Error())
					continue

				}
			}

			adapter.stanzaHandler(ctx, adapter.config, stanza, enqueueInput)
		}
	}
}

// superviseConnection is responsible for connection life cycle.
// This blocks till connection is closed.
//
// When connection is unintentionally closed, this returns an error to let caller handle the error and reconnect;
// when context is intentionally canceled, this returns nil so the caller can proceed to exit.
func (adapter *Adapter) superviseConnection(ctx context.Context, client *xmpp.Client, tryPing chan struct{}) error {
	ticker := time.NewTicker(adapter.config.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Context for this connection is closed. Simply return.
			return nil

		case message := <-adapter.messageQueue:
			_, err := client.Send(message)
			if err != nil {
				// Rather than enqueue ping via tryPing channel, try ping right away when message sending fails.
				// This is to make sure the following messages stay in the queue till connection check and reconnection is done
				pingErr := client.PingC2S("", "")
				if pingErr != nil {
					return fmt.Errorf("error on ping: %s", pingErr.Error())
				}
			}

		case <-ticker.C:
			select {
			case tryPing <- struct{}{}:
				// O.K

			default:
				// couldn't send because no goroutine is receiving channel or is busy.
				log.Info("Not sending ping signal because another goroutine already sent one and is being processed.")

			}

		case <-tryPing:
			err := client.PingC2S("", "")
			if err != nil {
				return fmt.Errorf("error on ping: %s", err.Error())
			}

		}
	}
}

func parseNick(jid string) string {
	s := strings.Split(jid, "@")
	if len(s) > 0 {
		s = strings.Split(s[1], "/")
		if len(s) == 2 {
			return s[1] // nick
		}
	}
	return ""
}

func (adapter *Adapter) parseChannel(remote string) string {
	s := strings.Split(remote, "@")
	if len(s) >= 2 {
		return s[0] // channel
	}
	return ""
}

// SendMessage let Bot send message to XMPP server
func (adapter *Adapter) SendMessage(ctx context.Context, output sarah.Output) {
	switch content := output.Content().(type) {
	case xmpp.Chat:
		adapter.messageQueue <- content

	case *sarah.CommandHelps:
		// TODO

	default:
		log.Warnf("unexpected output %#v", output)

	}
}

// NewStringResponse creates new sarah.CommandResponse instance with given string.
// This is a handy helper to setup outgoing xmpp.Chat value.
// To have more customized value, developers are encouraged to construct xmpp.Chat directly.
func NewStringResponse(input sarah.Input, message string) *sarah.CommandResponse {
	xmppInput, _ := input.(*MessageInput)
	return &sarah.CommandResponse{
		Content: xmpp.Chat{
			Remote: xmppInput.ReplyTo().(string),
			Text:   message,
			// https://tools.ietf.org/html/rfc3921#section-2.1.1
			// Although the 'type' attribute is OPTIONAL, it is considered polite to
			// mirror the type in any replies to a message; furthermore, some
			// specialized applications (e.g., a multi-user chat service) MAY at
			// their discretion enforce the use of a particular message type (e.g.,
			// type='groupchat').
			Type: xmppInput.Event.Type,
		},
	}
}

// NewStringResponseWithNext creates a new sarah.CommandResponse instance with given string and next function to continue.
// With this method, user context is directly stored as an anonymous function
// since XMPP Bot works with single connection and hence usually works with single process.
//
// To use external storage to store user context, use go-sarah-rediscontext or similar sarah.UserContextStorage implementation.
func NewStringResponseWithNext(input sarah.Input, message string, next sarah.ContextualFunc) *sarah.CommandResponse {
	xmppInput, _ := input.(*MessageInput)
	return &sarah.CommandResponse{
		Content: xmpp.Chat{
			Remote: xmppInput.ReplyTo().(string),
			Text:   message,
			// https://tools.ietf.org/html/rfc3921#section-2.1.1
			// Although the 'type' attribute is OPTIONAL, it is considered polite to
			// mirror the type in any replies to a message; furthermore, some
			// specialized applications (e.g., a multi-user chat service) MAY at
			// their discretion enforce the use of a particular message type (e.g.,
			// type='groupchat').
			Type: xmppInput.Event.Type,
		},
		UserContext: sarah.NewUserContext(next),
	}
}
