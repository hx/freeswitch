// This package provides a control interface into FreeSWITCH over its event socket layer.
//
// You can run API commands as you would in the CLI, and listen for events.
//
// If running on the same machine as FreeSWITCH, the client can read the event socket configuration file,
// so you won't need to provide connection information.
package freeswitch

import (
	"bufio"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultPort     uint16 = 8021
	defaultPassword        = "ClueCon"
	defaultHostname        = "localhost"
	defaultTimeout         = 5 * time.Second
)

// Represents a connection to FreeSWITCH's event socket layer. A zero Client is not valid; use NewClient().
type Client struct {
	// The hostname or IP address to which the client should connect (default "localhost").
	Hostname string

	// The port of the host machine to which the client should connect (default 8021).
	Port uint16

	// The FreeSWITCH event socket password (default "ClueCon").
	Password string

	// Timeout to use for connection and then for authentication (default 5 seconds).
	Timeout time.Duration

	//Optional. Called when sending and receiving data to/from FreeSWITCH.
	Logger func(packet string, isOutbound bool)

	conn     net.Conn
	inbox    chan *rawPacket
	outbox   chan chan *command
	errors   chan error
	reading  chan struct{}
	running  int32
	handlers map[EventName][]EventHandler
	control  sync.Mutex
	jobs     map[string]chan string // use jobsJock when reading/writing
	jobsLock sync.Mutex
}

// A function that can be registered to handle events.
type EventHandler func(Event)

type command struct {
	command  []string
	response chan packet
}

// Make a new client with the default hostname, password, port, and timeout. Client will attempt to read
// FreeSWITCH's event socket configuration file to obtain connection details.
func NewClient() (c *Client) {
	c = &Client{
		Hostname: defaultHostname,
		Password: defaultPassword,
		Port:     defaultPort,
		Timeout:  defaultTimeout,

		outbox:   make(chan chan *command),
		handlers: map[EventName][]EventHandler{},
	}
	c.handlers[EventName{"BACKGROUND_JOB", ""}] = []EventHandler{c.bgJobDone}
	c.guessConfiguration()
	return
}

// Connect to FreeSWITCH and block until disconnection. Call this method in its own goroutine, and call Shutdown()
// to make it return with no error.
func (c *Client) Connect() (err error) {
	c.control.Lock()
	defer c.control.Unlock()

	// Make sure we're not already connected.
	if c.conn != nil {
		return EAlreadyConnected
	}

	// Some sanity checks
	if c.Hostname == "" {
		return EBlankHostname
	}

	// Set up maps and channels
	c.reset()

	// Attempt TCP connection to FreeSWITCH
	if c.conn, err = net.DialTimeout("tcp", c.Hostname+":"+strconv.Itoa(int(c.Port)), c.Timeout); err == nil {

		// Start reading packets from FS and pumping them into the inbox channel. This process can be interrupted
		// by closing the connection, then waiting on the `reading` channel for it to exit.
		go c.consumePackets()

		// This timeout will cover authentication and event subscription
		handshakeTimeout := time.After(c.Timeout)

		// Wait the given timeout for FreeSWITCH to request authentication and, when requested, send it a password.
		select {
		case authPacket := <-c.inbox:
			if authPacket.packetType() == ptAuthRequest {
				err = c.write("auth", c.Password)
			} else {
				err = EUnexpectedResponse
			}
		case <-handshakeTimeout:
			err = ETimeout
		}

		// Still within the auth timeout, wait for an authentication response, and set an error if it fails.
		if err == nil {
			select {
			case authResponse := <-c.inbox:
				if result, ok := authResponse.cast().(*reply); !ok || !result.ok() {
					err = EAuthenticationFailed
				}
			case <-handshakeTimeout:
				err = ETimeout
			}
		}

		// Listen to events for already-defined event handlers.
		if err == nil && len(c.handlers) > 0 {
			names := make([]EventName, 0, len(c.handlers))
			for n := range c.handlers {
				names = append(names, n)
			}

			// Send the command and wait for FreeSWITCH to acknowledge the message
			if err = c.write(eventsSubscriptionCommand(names...)...); err == nil {
				select {
				case response := <-c.inbox:
					if result, ok := response.cast().(*reply); !ok || !result.ok() {
						err = ECommandFailed
					}
				case <-handshakeTimeout:
					err = ETimeout
				}
			}
		}

		// Begin normal operation
		if err == nil {

			// Commands will wait in this queue to receive their responses
			var cmdFiFo []*command

			// Allow other goroutines to put the client into an error state
			atomic.StoreInt32(&c.running, 1)

			// Allow other goroutines to take control of the client
			c.control.Unlock()

			// This is the normal operation loop
			for err == nil {
				cmdChan := make(chan *command)
				select {

				// This will break the loop
				case err = <-c.errors:

				// We've received an inbound packet from FreeSWITCH
				case inbound := <-c.inbox:
					switch p := inbound.cast().(type) {
					case *inboundEvent:
						var handlers []EventHandler
						exclusive(&c.control, func() {
							handlers = c.handlers[*p.Name()][:]
						})
						for _, handler := range handlers {
							go handler(p) // Rely on handlers to recover from their own panics
						}
					case *disconnectNotice:
						err = EDisconnected
					default:
						if len(cmdFiFo) > 0 {
							cmd := cmdFiFo[0]
							cmdFiFo = cmdFiFo[1:]
							cmd.response <- p
						}
						// Discard other packets
					}

				// A goroutine wants to write to the connection. During connection and handshake, it'll block until
				// it gets one of these.
				case c.outbox <- cmdChan:
					cmd := <-cmdChan
					cmdFiFo = append(cmdFiFo, cmd)
					err = c.write(cmd.command...)
				}
			}

			// If the loop set an error, there may also be an error trying to get into the error channel
			if !atomic.CompareAndSwapInt32(&c.running, 1, 0) {

				// The only error from this channel that should be preferred over one from the loop is an EShutdown
				if <-c.errors == EShutdown {
					err = EShutdown
				}
			}

			// Take control back from other goroutines
			c.control.Lock()

			// Cancel pending commands that haven't yet received their responses
			for _, cmd := range cmdFiFo {
				cmd.response <- nil
			}
		}

		// Close the connection
		c.conn.Close()
		c.conn = nil

		// Wait for the consumePackets() goroutine to finish
		<-c.reading
	}

	// Normalise the exit error.
	if err == EShutdown {
		err = nil
	}
	return
}

// Close the connection to FreeSWITCH and return from Connect().
func (c *Client) Shutdown() {
	c.control.Lock()
	defer c.control.Unlock()
	c.close(EShutdown)
	// No need to block here. Another connection attempt will wait for the control lock.
}

// Handle the given event with the given handler. It can be called multiple times to register multiple handlers, which
// will be called simultaneously when an event fires. For CUSTOM events, use OnCustom() instead.
func (c *Client) On(eventName string, handler EventHandler) {
	c.on(EventName{eventName, ""}, handler)
}

// Handle custom events. See On() for details.
func (c *Client) OnCustom(eventSubclass string, handler EventHandler) {
	c.on(EventName{"CUSTOM", eventSubclass}, handler)
}

// Run an API command, and get the response as a string.
//
// This is a blocking (synchronous) method. If you want to discard the result, or execute a call asynchronously, use
// Query().
func (c *Client) Execute(app string, args ...string) (result string, err error) {
	return c.execute(append([]string{"api", app}, args...))
}

func (c *Client) execute(args []string) (result string, err error) {
	var ch chan *command
	ch, err = c.startCommand()
	if err == nil {
		err = ENotConnected
		cmd := newCommand(args)
		ch <- cmd
		if response := <-cmd.response; response != nil {
			return response.String(), nil
		}
	}
	return
}

// Same as Execute(), but panics if an error occurs.
func (c *Client) MustExecute(app string, args ...string) string {
	result, err := c.Execute(app, args...)
	if err != nil {
		panic(err)
	}
	return result
}

// Run an API command, and get the response as a string through the returned channel.
//
// See Execute(). This method is identical, but returns a channel through which the result will eventually be passed.
func (c *Client) Query(app string, args ...string) (result chan string, err error) {
	var ch chan *command
	ch, err = c.startCommand()
	jobId := uniqId()
	if err == nil {
		err = ENotConnected
		cmd := newCommand(append([]string{"bgapi", app}, args...))
		cmd.command[(len(cmd.command) - 1)] += "\nJob-UUID: " + jobId
		result = make(chan string, 1)
		exclusive(&c.jobsLock, func() { c.jobs[jobId] = result })
		ch <- cmd
		if response := <-cmd.response; response != nil {
			return result, nil
		}
		result = nil
	}
	exclusive(&c.jobsLock, func() { delete(c.jobs, jobId) })
	return
}

// Same as Query(), but panics if an error occurs.
func (c *Client) MustQuery(app string, args ...string) chan string {
	result, err := c.Query(app, args...)
	if err != nil {
		panic(err)
	}
	return result
}

func (c *Client) bgJobDone(e Event) {
	var (
		resultChan chan string
		jobId      = e.Get("Job-UUID")
	)
	if jobId != "" {
		exclusive(&c.jobsLock, func() {
			resultChan = c.jobs[jobId]
			delete(c.jobs, jobId)
		})
		if resultChan != nil {
			resultChan <- e.Body()
		}
	}
}

func (c *Client) startCommand() (ch chan *command, err error) {
	select {
	case ch = <-c.outbox:
	case <-time.After(c.Timeout):
		err = ETimeout
	}
	return
}

func (c *Client) write(cmd ...string) (err error) {
	joined := strings.Join(cmd, " ")
	c.log(joined, true)
	_, err = c.conn.Write(append([]byte(joined), '\n', '\n'))
	return
}

func (c *Client) reset() {
	c.jobs = map[string]chan string{}
	c.inbox = make(chan *rawPacket)
	c.errors = make(chan error)
	c.reading = make(chan struct{})
}

func (c *Client) on(name EventName, handler EventHandler) (err error) {
	c.control.Lock()
	alreadyHandled := len(c.handlers[name]) > 0
	c.handlers[name] = append(c.handlers[name], handler)
	connected := c.conn != nil
	c.control.Unlock()
	if connected && !alreadyHandled {
		_, err = c.execute(eventsSubscriptionCommand(name))
	}
	return
}

func (c *Client) close(err error) {
	if atomic.CompareAndSwapInt32(&c.running, 1, 0) {
		c.errors <- err
	}
}

func (c *Client) consumePackets() {
	var (
		mainReader = bufio.NewReader(c.conn)
		mimeReader = textproto.NewReader(mainReader)
	)
	for {
		headers, err := mimeReader.ReadMIMEHeader()
		if err != nil {
			c.close(err)
			break
		}
		p := &rawPacket{headers: loadHeaders(headers, false)}
		if contentLengthStr := p.headers.get("Content-Length"); contentLengthStr != "" {
			contentLength, err := strconv.Atoi(contentLengthStr)
			if err != nil {
				c.close(err)
				break
			}
			if contentLength > 0 {
				body := make([]byte, contentLength)
				if _, err := io.ReadFull(mainReader, body); err != nil {
					c.close(err)
					break
				}
				p.body = string(body)
			}
		}
		c.log(p.String(), false)
		c.inbox <- p
	}
	c.reading <- struct{}{}
}

func (c *Client) log(packet string, isOutbound bool) {
	if logger := c.Logger; logger != nil {
		logger(packet, isOutbound)
	}
}
