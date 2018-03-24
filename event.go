package freeswitch

import (
	"bufio"
	"io"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Represents an event raised by FreeSWITCH, and will be passed to your event handlers.
type Event interface {
	// Get a value from the event body's headers. An empty string will be returned if the given name
	// is not present in the event.
	Get(string) string

	// Get the event's body, excluding its headers, as a string.
	Body() string

	// Returns the name of the event as an EventName.
	Name() *EventName

	// The time the event was raised, based on the Event-Date-Timestamp header of the event.
	Timestamp() *time.Time
}

// The name and optional subclass of a raised event.
type EventName struct {
	Name     string
	Subclass string
}

type inboundEvent struct {
	*rawPacket
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

func (ie *inboundEvent) Name() *EventName {
	return &EventName{
		Name:     ie.Get("Event-Name"),
		Subclass: ie.Get("Event-Subclass"),
	}
}

func (ie *inboundEvent) Get(name string) string {
	ie.read()
	if ie.headers == nil {
		return ""
	}
	return ie.headers.get(name)
}

func (ie *inboundEvent) Timestamp() (ts *time.Time) {
	timestamp, err := strconv.Atoi(ie.Get("Event-Date-Timestamp"))
	if err == nil {
		t := time.Unix(int64(timestamp/1e6), int64(timestamp%1e6*1e3))
		ts = &t
	}
	return
}

func (ie *inboundEvent) Body() string {
	ie.read()
	return ie.body
}

func (ie *inboundEvent) read() {
	if ie.headers == nil {
		reader := bufio.NewReader(strings.NewReader(ie.rawPacket.body))
		mimeHeaders, err := textproto.NewReader(reader).ReadMIMEHeader()
		if err == nil || err == io.EOF {
			ie.headers = loadHeaders(mimeHeaders, true)
			if contentLength, err := strconv.Atoi(ie.headers.get("Content-Length")); err == nil && contentLength > 0 {
				body := make([]byte, contentLength)
				io.ReadFull(reader, body)
				ie.body = string(body)
			}
		} else {
			ie.headers = headers{&header{"Event-Name", err.Error()}}
		}
	}
}
