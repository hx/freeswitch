package freeswitch

import (
	"bufio"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Represents an event raised by FreeSWITCH, and will be passed to your event handlers.
type Event interface {
	Get(string) string
	Name() *EventName
	Timestamp() *time.Time
}

// The name and optional subclass of a raised event.
type EventName struct {
	Name     string
	Subclass string
}

type inboundEvent struct {
	*rawPacket
	body headers
}

// Returns true if the event name is a custom event (and therefore should have a subclass).
func (en *EventName) IsCustom() bool {
	return en.Subclass != ""
}

func (en *EventName) String() string {
	if en.IsCustom() {
		return en.Name + " " + en.Subclass
	} else {
		return en.Name
	}
}

// Returns the name of the event as an EventName.
func (ie *inboundEvent) Name() *EventName {
	return &EventName{
		Name:     ie.Get("Event-Name"),
		Subclass: ie.Get("Event-Subclass"),
	}
}

// Get a value from the event body. An empty string will be returned if the given name is not present in the event.
func (ie *inboundEvent) Get(name string) string {
	if ie.body == nil {
		headers, err := textproto.NewReader(bufio.NewReader(strings.NewReader(ie.rawPacket.body))).ReadMIMEHeader()
		if err != nil {
			ie.body = loadHeaders(headers, true)
		}
	}
	if ie.body == nil {
		return ""
	}
	return ie.body.get(name)
}

// The time the event was raised, based on the Event-Date-Timestamp header of the event.
func (ie *inboundEvent) Timestamp() (ts *time.Time) {
	timestamp, err := strconv.Atoi(ie.Get("Event-Date-Timestamp"))
	if err == nil {
		t := time.Unix(int64(timestamp/1e6), int64(timestamp%1e6*1e3))
		ts = &t
	}
	return
}
