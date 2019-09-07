package xmpp

import (
	"crypto/tls"
	"golang.org/x/xerrors"
	"strings"
	"time"

	"github.com/mattn/go-xmpp"
	"github.com/oklahomer/go-sarah/v2"
	"github.com/oklahomer/go-sarah/v2/log"

	"context"
	"fmt"
	"github.com/oklahomer/go-sarah/retry"
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

type Stanza interface{}

type StanzaHandler func(ctx context.Context, config *Config, stanza Stanza, enqueueInput func(sarah.Input) error)

func defaultStanzaHandler(_ context.Context, config *Config, stanza Stanza, enqueueInput func(input sarah.Input) error) {
	switch typedStanza := stanza.(type) {
	case xmpp.Chat:
		if trimResource(typedStanza.Remote) == trimResource(config.Jid) {
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
			help := sarah.NewHelpInput(input)
			enqueueInput(help)
		} else if config.AbortCommand != "" && trimmed == config.AbortCommand {
			// Abort command
			abort := sarah.NewAbortInput(input)
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
		stanzaHandler: defaultStanzaHandler, // Can be overridden by AdapterOption
		messageQueue:  make(chan xmpp.Chat, config.SendingQueueSize),
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

// Set up and watch the XMPP connection
func (adapter *Adapter) Run(ctx context.Context, enqueueInput func(sarah.Input) error, notifyErr func(error)) {
	for {
		client, err := connect(adapter.config)
		if err != nil {
			// Failed to connect with configured retrial settings.
			// Notify non-continuable situation to sarah.Runner and let this adapter stop.
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

func connect(config *Config) (*xmpp.Client, error) {
	tc := new(tls.Config)
	tc.InsecureSkipVerify = config.SkipTLSVerify
	tc.ServerName = strings.Split(config.Server, ":")[0]
	options := xmpp.Options{
		Host:                         config.Server,
		User:                         config.Jid,
		Password:                     config.Password,
		NoTLS:                        config.NoTLS,
		StartTLS:                     config.StartTLS,
		TLSConfig:                    tc,
		Debug:                        config.Debug,
		Session:                      config.Session,
		Status:                       config.Status,
		StatusMessage:                config.StatusMessage,
		Resource:                     config.Resource,
		InsecureAllowUnencryptedAuth: config.InsecureAllowUnencryptedAuth,
		OAuthScope:                   config.OAuthScope,
		OAuthToken:                   config.OAuthToken,
		OAuthXmlNs:                   config.OAuthXmlNs,
	}

	var client *xmpp.Client
	err := retry.WithInterval(config.RetryLimit, func() (e error) {
		client, e = options.NewClient()
		return e
	}, config.RetryInterval)
	if err != nil {
		return nil, err
	}

	return client, nil
}

type stanzaReceiver interface {
	Recv() (interface{}, error)
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
func (adapter *Adapter) receiveStanza(ctx context.Context, client stanzaReceiver, tryPing chan<- struct{}, enqueueInput func(sarah.Input) error) {
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

type stanzaSender interface {
	Send(chat xmpp.Chat) (int, error)
	PingC2S(string, string) error
}

// superviseConnection is responsible for connection life cycle.
// This blocks till connection is closed.
//
// When connection is unintentionally closed, this returns an error to let caller handle the error and reconnect;
// when context is intentionally canceled, this returns nil so the caller can proceed to exit.
func (adapter *Adapter) superviseConnection(ctx context.Context, client stanzaSender, tryPing chan struct{}) error {
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

// NewResponse creates *sarah.CommandResponse with given arguments.
// Simply pass a given sarah.Input instance and a text string to send a string message as a reply.
// To send a more complicated reply message, pass as many options created by ResponseWith* function as required.
func NewResponse(input sarah.Input, message string, options ...RespOption) (*sarah.CommandResponse, error) {
	messageInput, ok := input.(*MessageInput)
	if !ok {
		return nil, xerrors.Errorf("%T is not currently supported to automatically generate response", input)
	}

	stash := &respOptions{}
	for _, opt := range options {
		opt(stash)
	}

	return &sarah.CommandResponse{
		Content: xmpp.Chat{
			Remote: messageInput.ReplyTo().(string),
			Text:   message,

			// https://tools.ietf.org/html/rfc3921#section-2.1.1
			// Although the 'type' attribute is OPTIONAL, it is considered polite to
			// mirror the type in any replies to a message; furthermore, some
			// specialized applications (e.g., a multi-user chat service) MAY at
			// their discretion enforce the use of a particular message type (e.g.,
			// type='groupchat').
			Type: messageInput.Event.Type,

			// Inherit the given thread identifier by default to indicate that this response is part of the thread.
			// Developer may ignore the value or use different thread identifier by constructing sarah.CommandResponse manually.
			Thread: messageInput.Event.Thread,
		},
		UserContext: stash.userContext,
	}, nil
}

// RespOption defines function signature that NewResponse's functional option must satisfy.
type RespOption func(*respOptions)

type respOptions struct {
	userContext *sarah.UserContext
}

// RespWithNext sets given fnc as part of the response's *sarah.UserContext.
// The next input from the same user will be passed to this fnc.
// See sarah.UserContextStorage must be present or otherwise, fnc will be ignored.
func RespWithNext(fnc sarah.ContextualFunc) RespOption {
	return func(options *respOptions) {
		options.userContext = &sarah.UserContext{
			Next: fnc,
		}
	}
}

// RespWithNextSerializable sets given arg as part of the response's *sarah.UserContext.
// The next input from the same user will be passed to the function defined in the arg.
// See sarah.UserContextStorage must be present or otherwise, arg will be ignored.
func RespWithNextSerializable(arg *sarah.SerializableArgument) RespOption {
	return func(options *respOptions) {
		options.userContext = &sarah.UserContext{
			Serializable: arg,
		}
	}
}
