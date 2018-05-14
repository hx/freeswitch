package freeswitch

import (
	"bufio"
	"io"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// The name and optional subclass of a raised event.
type EventName struct {
	Name     string
	Subclass string
}

// Represents an event raised by FreeSWITCH, and will be passed to your event handlers.
type Event struct {
	*rawPacket
	client  *Client
	headers headers
	body    string
}

// Returns true if the event name is a custom event (and therefore should have a subclass).
func (en *EventName) IsCustom() bool {
	return en.Subclass != ""
}

// The name of the event as a string, including the "CUSTOM " prefix for custom events.
func (en *EventName) String() string {
	if en.IsCustom() {
		return en.Name + " " + en.Subclass
	}
	return en.Name
}

// Returns the name of the event as an EventName.
func (e *Event) Name() *EventName {
	return &EventName{
		Name:     e.Get("Event-Name"),
		Subclass: e.Get("Event-Subclass"),
	}
}

// Get a value from the event body's headers. An empty string will be returned if the given name
// is not present in the event.
func (e *Event) Get(name string) string {
	e.read()
	if e.headers == nil {
		return ""
	}
	return e.headers.get(name)
}

// Set a value in the event body's header. Empty values will no-op.
func (e *Event) Set(name string, value string) *Event {
	if value != "" {
		e.read()
		if e.headers == nil {
			e.headers = headers{}
		}
		e.headers.set(name, value)
	}
	return e
}

// Set the event's body.
func (e *Event) SetBody(value string) *Event {
	if value != e.body {
		e.read()
		e.Set("Content-Length", strconv.Itoa(len(value))).body = value
	}
	return e
}

// The time the event was raised, based on the Event-Date-Timestamp header of the event.
func (e *Event) Timestamp() (ts *time.Time) {
	timestamp, err := strconv.Atoi(e.Get("Event-Date-Timestamp"))
	if err == nil {
		t := time.Unix(int64(timestamp/1e6), int64(timestamp%1e6*1e3))
		ts = &t
	}
	return
}

// Get the event's body, excluding its headers, as a string.
func (e *Event) Body() string {
	e.read()
	return e.body
}

// Send the event into FreeSWITCH, and set the Event-UUID header from the resulting event.
func (e *Event) Send() error {
	e.read()
	return e.client.sendEvent(e)
}

// Get the event's Event-Sequence number. Return value will be zero if no Event-Sequence header is present.
func (e *Event) Sequence() int {
	num, _ := strconv.Atoi(e.Get("Event-Sequence"))
	return num
}

// Dump the event out to a string, as it would be sent to/from FreeSWITCH.
func (e *Event) String() string {
	e.read()
	return e.headers.escapedString() + "\n" + e.Body()
}

func (e *Event) read() {
	if e.headers == nil {
		reader := bufio.NewReader(strings.NewReader(e.rawPacket.body))
		mimeHeaders, err := textproto.NewReader(reader).ReadMIMEHeader()
		if err == nil || err == io.EOF {
			e.headers = loadHeaders(mimeHeaders, true)
			if contentLength, err := strconv.Atoi(e.headers.get("Content-Length")); err == nil && contentLength > 0 {
				body := make([]byte, contentLength)
				io.ReadFull(reader, body)
				e.body = string(body)
			}
		} else {
			e.headers = headers{&header{"Event-Name", err.Error()}}
		}
	}
}
