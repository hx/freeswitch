// This package provides a control interface into FreeSWITCH over its event socket layer.
//
// You can run API commands as you would in the CLI, and listen for events.
//
// If running on the same machine as FreeSWITCH, the client can read the event socket configuration file,
// so you won't need to provide connection information.
package freeswitch

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultPort     uint16 = 8021
	defaultPassword        = "ClueCon"
	defaultHostname        = "localhost"
	defaultTimeout         = 3 * time.Second
)

// Represents a connection to FreeSWITCH's event socket. A zero client is valid, and if its Open() method is called,
// it will use the default hostname, port, and password values of localhost, 8021, and ClueCon.
type Client struct {

	// Optional. Called when the client is disconnected without calling Close().
	OnDisconnect func(error)

	// Optional. Called when an event handler panics.
	OnEventHandlerPanic func(event Event, handler EventHandler, err interface{})

	//Optional. Called when sending and receiving data to/from FreeSWITCH.
	Logger func(packet string, isOutbound bool)

	hostname string
	port     uint16
	password *string
	timeout  time.Duration

	conn        net.Conn
	execLock    sync.Mutex
	controlLock sync.Mutex
	replyLock   sync.Mutex

	handlers map[EventName][]EventHandler
	inbox    chan packet
	events   chan Event
	replies  map[string]chan string
}

// A function that can be registered to handle events.
type EventHandler func(Event)

// Connect to and authenticate with the FreeSWITCH event socket. An error will be returned if connection or
// authentication fails.
func (c *Client) Open() error {
	c.controlLock.Lock()
	defer c.controlLock.Unlock()

	if c.conn != nil {
		return EAlreadyConnected
	}
	if c.port == 0 {
		c.port = defaultPort
	}
	if c.hostname == "" {
		c.hostname = defaultHostname
	}
	if c.password == nil {
		password := defaultPassword
		c.password = &password
	}
	if c.timeout == 0 {
		c.timeout = defaultTimeout
	}
	conn, err := net.DialTimeout("tcp", c.hostname+":"+strconv.Itoa(int(c.port)), c.timeout)
	if err != nil {
		return err
	}

	c.conn = conn
	c.inbox = make(chan packet)

	go c.consumePackets()

	select {
	case authPacket := <-c.inbox:
		if authPacket.packetType() == ptAuthRequest {
			if result, ok := c.exec("auth", *c.password).(*reply); ok {
				if !result.ok() {
					return EAuthenticationFailed
				}
			} else {
				return EUnexpectedResponse
			}
		} else {
			return EUnexpectedResponse
		}
	case <-time.After(c.timeout):
		return ETimeout
	}

	c.consumeEvents()

	return nil
}

// Close the connection to FreeSWITCH.
func (c *Client) Close() error {
	c.controlLock.Lock()
	defer c.controlLock.Unlock()

	if c.conn == nil {
		return ENotConnected
	}
	return c.shutdown()
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

func (c *Client) on(name EventName, handler EventHandler) {
	c.controlLock.Lock()
	if c.handlers == nil {
		c.handlers = make(map[EventName][]EventHandler)
	}
	if c.conn != nil {
		c.consumeEvents()
	}
	c.handlers[name] = append(c.handlers[name], handler)
	c.controlLock.Unlock()
	c.exec("events plain", name.String())
}

// Run an API command, and get the response as a string.
//
// This is a blocking (synchronous) method. If you want to discard the result, or execute a call asynchronously, use
// Query().
func (c *Client) Execute(command string, args ...string) string {
	if result := c.exec(append([]string{"api", command}, args...)...); result == nil {
		return ""
	} else {
		return result.String()
	}
}

// Run an API command, and get the response as a string through the returned channel.
//
// See Execute(). This method is identical, but returns a channel through which the result will eventually be passed.
func (c *Client) Query(command string, args ...string) (result chan string) {
	c.consumeBackgroundJobs()
	result = make(chan string, 1)
	c.replyLock.Lock()
	defer c.replyLock.Unlock()
	if response := c.exec(append([]string{"bgapi", command}, args...)...); response != nil {
		if j, ok := response.(*reply); ok && j.ok() {
			if jobId := j.jobId(); jobId != "" {
				c.replies[jobId] = result
				return
			}
		}
	}
	result <- ""
	c.fatal(ECommandFailed)
	return
}

// Set the FreeSWITCH hostname, port, password, and connection timeout manually prior to opening a connection.
func (c *Client) Configure(hostname string, port int, password string, timeout time.Duration) error {
	if port <= 0 || port >= 1<<16 {
		return EInvalidPort
	}
	if hostname == "" {
		return EBlankHostname
	}
	c.hostname = hostname
	c.password = &password
	c.timeout = timeout
	c.port = uint16(port)

	return nil
}

func (c *Client) consumeBackgroundJobs() {
	c.replyLock.Lock()
	defer c.replyLock.Unlock()
	if c.replies == nil {
		c.replies = make(map[string]chan string)
		c.On("BACKGROUND_JOB", func(e Event) {
			c.replyLock.Lock()
			defer c.replyLock.Unlock()
			if jobId := e.Get("Job-UUID"); jobId != "" {
				if replyChan := c.replies[jobId]; replyChan != nil {
					delete(c.replies, jobId)
					replyChan <- e.Body()
				}
			}
		})
	}
}

func (c *Client) shutdown() (err error) {
	if c.conn != nil {
		err = c.conn.Close()
		c.conn = nil
	}
	if c.inbox != nil {
		close(c.inbox)
		c.inbox = nil
	}
	if c.events != nil {
		close(c.events)
		c.events = nil
	}
	if c.replies != nil {
		c.replies = make(map[string]chan string)
	}
	return
}

func (c *Client) fatal(err error) {
	c.controlLock.Lock()
	defer c.controlLock.Unlock()

	wasOpen := c.conn != nil

	c.shutdown()

	if h := c.OnDisconnect; h != nil && wasOpen {
		go h(err)
	}
}

func (c *Client) exec(words ...string) (result packet) {
	if c.conn == nil {
		panic(ENotConnected) // TODO not this
	}
	c.execLock.Lock()
	defer c.execLock.Unlock()
	command := strings.Join(words, " ")
	c.log(command, true)
	if _, err := c.conn.Write(append([]byte(command), '\n', '\n')); err == nil {
		result = <-c.inbox
	} else {
		c.fatal(err)
	}
	return
}

func (c *Client) log(packet string, isOutbound bool) {
	if logger := c.Logger; logger != nil {
		logger(packet, isOutbound)
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
			c.fatal(err)
			return
		}
		p := &rawPacket{headers: loadHeaders(headers, false)}
		if contentLengthStr := p.headers.get("Content-Length"); contentLengthStr != "" {
			contentLength, err := strconv.Atoi(contentLengthStr)
			if err != nil {
				c.fatal(err)
				return
			}
			if contentLength > 0 {
				body := make([]byte, contentLength)
				_, err := io.ReadFull(mainReader, body)
				if err != nil {
					c.fatal(err)
					return
				}
				p.body = string(body)
			}
		}
		c.log(p.String(), false)
		switch p := p.cast().(type) {
		case *inboundEvent:
			if c.events != nil {
				c.events <- p
			}
		case *disconnectNotice:
			c.fatal(errors.New(p.String()))
			return
		default:
			c.inbox <- p
		}
	}
}

func (c *Client) consumeEvents() {
	if c.events == nil && c.conn != nil && c.handlers != nil {
		c.events = make(chan Event)
		if len(c.handlers) > 0 {
			var (
				normal []string
				custom []string
			)
			for name, handlers := range c.handlers {
				if len(handlers) > 0 {
					if name.IsCustom() {
						custom = append(custom, name.Subclass)
					} else {
						normal = append(normal, name.Name)
					}
				}
			}
			args := append([]string{"events plain"}, normal...)
			if len(custom) > 0 {
				args = append(args, append([]string{"CUSTOM"}, custom...)...)
			}
			c.exec(args...)
		}
		events := c.events // Avoids a race with the calling function
		go func() {
			for event := range events {
				c.controlLock.Lock()
				for _, handler := range c.handlers[*event.Name()] {
					go func(e Event, h EventHandler) {
						defer func() {
							if err := recover(); err != nil {
								if ph := c.OnEventHandlerPanic; ph != nil {
									ph(e, h, err)
								}
							}
						}()
						h(e)
					}(event, handler)
				}
				c.controlLock.Unlock()
			}
		}()
	}
}
