// Package freeswitch provides a control interface into FreeSWITCH over its event socket layer.
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

// Client represents a connection to FreeSWITCH's event socket layer. A zero Client is not valid; use NewClient().
type Client struct {
	// The hostname or IP address to which the client should connect (default "localhost").
	Hostname string

	// The port of the host machine to which the client should connect (default 8021).
	Port uint16

	// The FreeSWITCH event socket password (default "ClueCon").
	Password string

	// Timeout to use for connection and then for authentication (default 5 seconds).
	Timeout time.Duration

	// Optional. Called when sending and receiving data to/from FreeSWITCH.
	Logger func(packet string, isOutbound bool)

	// Advanced. If true, only "bgapi" commands will be used. This will not affect the client's behaviour, but
	// may affect performance of FreeSWITCH (for better or worse). If in doubt, leave it false. To avoid races,
	// don't change its value while connected.
	PreventSocketBlocking bool

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

// EventHandler is a function that can be registered to handle events.
type EventHandler func(Event)

type command struct {
	command  []string
	response chan packet
}

// NewClient makes a new client with the default hostname, password, port, and timeout. It will attempt to read
// FreeSWITCH's event socket configuration file to obtain connection details.
func NewClient() *Client {
	c := &Client{
		Hostname: defaultHostname,
		Password: defaultPassword,
		Port:     defaultPort,
		Timeout:  defaultTimeout,

		inbox:    make(chan *rawPacket),
		outbox:   make(chan chan *command),
		handlers: map[EventName][]EventHandler{},
		jobs:     map[string]chan string{},
		errors:   make(chan error),
		reading:  make(chan struct{}),
	}
	c.handlers[EventName{"BACKGROUND_JOB", ""}] = []EventHandler{c.bgJobDone}
	c.guessConfiguration()
	return c
}

// Connect to FreeSWITCH and block until disconnection. Call this method in its own goroutine, and call Shutdown()
// to make it return with no error.
func (c *Client) Connect() (err error) {
	c.control.Lock()
	defer c.control.Unlock()

	// Make sure we're not already connected.
	if !c.setRunning(true) {
		return EAlreadyConnected
	}

	// Some sanity checks
	if c.Hostname == "" {
		return EBlankHostname
	}

	// Attempt TCP connection to FreeSWITCH
	if c.conn, err = net.DialTimeout("tcp", c.Hostname+":"+strconv.Itoa(int(c.Port)), c.Timeout); err == nil {

		// Start reading packets from FS and pumping them into the inbox channel. This process can be interrupted
		// by closing the connection, then waiting on the `reading` channel for it to exit.
		go c.read()

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

			// Take control back from other goroutines
			c.control.Lock()

			// Cancel pending commands that haven't yet received their responses
			for _, cmd := range cmdFiFo {
				cmd.response <- nil
			}

			// Unblock background jobs with empty responses
			exclusive(&c.jobsLock, func() {
				if len(c.jobs) > 0 {
					for _, job := range c.jobs {
						job <- ""
					}
					c.jobs = map[string]chan string{}
				}
			})

			// Tell goroutines waiting to send commands that we're closed for the day
			for done := false; !done; {
				select {
				case ch := <-c.outbox:
					ch <- nil
				default:
					done = true
				}
			}
		}

		// If the loop set an error, there may also be an error trying to get into the error channel
		if !c.setRunning(false) {

			// The only error from this channel that should be preferred over one from the loop is an EShutdown
			if <-c.errors == EShutdown {
				err = EShutdown
			}
		}

		// Close the connection
		c.conn.Close()

		// Wait for the read() goroutine to finish
		<-c.reading
	} else {
		c.setRunning(false)
	}

	// Normalise the exit error.
	if err == EShutdown {
		err = nil
	}
	return
}

// Shutdown will close the connection to FreeSWITCH and return from Connect().
func (c *Client) Shutdown() {
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
//
// Internally, this method uses the "api" command. If PreventSocketBlocking is true, it will use "bgapi" instead, and
// block until a response is received. Either way, its behaviour should be the same.
func (c *Client) Execute(app string, args ...string) (result string, err error) {
	if c.PreventSocketBlocking {
		ch, err := c.Query(app, args...)
		if err == nil {
			result = <-ch
		}
	} else {
		var p packet
		p, err = c.execute(append([]string{"api", app}, args...))
		if p != nil {
			result = p.String()
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
// If the connection is interrupted or the command results in an error, an empty string will be sent through the
// returned channel.
func (c *Client) Query(app string, args ...string) (result chan string, err error) {
	var (
		jobID = uniqueID()
		cmd   = app + " " + strings.Join(args, " ") + "\nJob-UUID: " + jobID
		p     packet
	)
	result = make(chan string, 1)
	exclusive(&c.jobsLock, func() { c.jobs[jobID] = result })
	p, err = c.execute([]string{"bgapi", cmd})
	if p == nil {
		result = nil
		exclusive(&c.jobsLock, func() { delete(c.jobs, jobID) })
	}
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

func (c *Client) execute(args []string) (result packet, err error) {
	var ch chan *command
	select {
	case ch = <-c.outbox:
		if ch == nil {
			err = ENotConnected
		}
	case <-time.After(c.Timeout):
		err = ETimeout
	}
	if err == nil {
		cmd := &command{
			command:  args,
			response: make(chan packet),
		}
		ch <- cmd
		result = <-cmd.response
		if result == nil {
			err = ENotConnected
		}
	}
	return
}

func (c *Client) bgJobDone(e Event) {
	var (
		resultChan chan string
		jobID      = e.Get("Job-UUID")
	)
	if jobID != "" {
		exclusive(&c.jobsLock, func() {
			resultChan = c.jobs[jobID]
			delete(c.jobs, jobID)
		})
		if resultChan != nil {
			resultChan <- e.Body()
		}
	}
}

func (c *Client) write(cmd ...string) (err error) {
	joined := strings.Join(cmd, " ")
	c.log(joined, true)
	_, err = c.conn.Write(append([]byte(joined), '\n', '\n'))
	return
}

func (c *Client) on(name EventName, handler EventHandler) (err error) {
	c.control.Lock()
	alreadyHandled := len(c.handlers[name]) > 0
	c.handlers[name] = append(c.handlers[name], handler)
	c.control.Unlock()
	if c.isRunning() && !alreadyHandled {
		_, err = c.execute(eventsSubscriptionCommand(name))
	}
	return
}

func (c *Client) close(err error) {
	if c.setRunning(false) {
		c.errors <- err
	}
}

func (c *Client) setRunning(running bool) (changed bool) {
	var old int32
	if !running {
		old = 1
	}
	return atomic.CompareAndSwapInt32(&c.running, old, 1-old)
}

func (c *Client) isRunning() bool {
	return atomic.LoadInt32(&c.running) == 1
}

func (c *Client) read() {
	var (
		mainReader    = bufio.NewReader(c.conn)
		mimeReader    = textproto.NewReader(mainReader)
		err           error
		headers       textproto.MIMEHeader
		contentLength int
	)
	for err == nil {
		if headers, err = mimeReader.ReadMIMEHeader(); err == nil {
			p := &rawPacket{headers: loadHeaders(headers, false)}
			if contentLengthStr := p.headers.get("Content-Length"); contentLengthStr != "" {
				if contentLength, err = strconv.Atoi(contentLengthStr); err == nil && contentLength > 0 {
					body := make([]byte, contentLength)
					if _, err = io.ReadFull(mainReader, body); err == nil {
						p.body = string(body)
					}
				}
			}
			c.log(p.String(), false)
			c.inbox <- p
		}
	}
	c.close(err)
	c.reading <- struct{}{}
}

func (c *Client) log(packet string, isOutbound bool) {
	if logger := c.Logger; logger != nil {
		logger(packet, isOutbound)
	}
}
