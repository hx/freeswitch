package freeswitch

import (
	"net/textproto"
	"net/url"
	"strings"
)

// A collection of event headers.
type headers []*header

// Represents a single header in an event.
type header struct {
	name  string
	value string
}

func (h *headers) add(name, value string) {
	n := append(*h, &header{name, value})
	*h = n
}

func (h *headers) set(name, value string) {
	h.del(name)
	h.add(name, value)
}

func (h headers) get(name string) string {
	for _, header := range h {
		if header.matchName(name) {
			return header.value
		}
	}
	return ""
}

func (h *headers) del(name string) {
	if count := len(h.getAll(name)); count > 0 {
		o := *h
		n := make(headers, 0, len(o)-count)
		for _, header := range o {
			if !header.matchName(name) {
				n = append(n, header)
			}
		}
		*h = n
	}
}

func (h headers) getAll(name string) (values []string) {
	for _, header := range h {
		if header.matchName(name) {
			values = append(values, header.value)
		}
	}
	return
}

// Returns headers in plain text format, e.g.:
//	Event-Date-Local: 2018-05-04 04:06:45\n
//	Event-Sequence: 79878\n
//	...etc
func (h headers) String() (str string) {
	for _, header := range h {
		str += header.String()
	}
	return
}

// Same as the String method, but percent-escapes the value parts:
//	Event-Date-Local: 2018-05-04%2004%3A06%3A45\n
//	Event-Sequence: 79878\n
//	...etc
func (h headers) escapedString() (str string) {
	for _, header := range h {
		str += header.escapedString()
	}
	return
}

func (h *headers) load(mh textproto.MIMEHeader, unescape bool) {
	for name, values := range mh {
		for _, value := range values {
			if unescape {
				value, _ = url.PathUnescape(value)
			}
			h.add(name, value)
		}
	}
}

func (h *header) matchName(name string) bool {
	return strings.ToLower(name) == strings.ToLower(h.name)
}

// Returns the header in plain text format, e.g.:
//	Event-Date-Local: 2018-05-04 04:06:45\n
func (h *header) String() string {
	return h.name + ": " + h.value + "\n"
}

func (h *header) escapedValue() string {
	return url.PathEscape(h.value)
}

// Same as the String method, but percent-escapes the value part:
//	Event-Date-Local: 2018-05-04%2004%3A06%3A45\n
func (h *header) escapedString() string {
	return h.name + ": " + h.escapedValue() + "\n"
}

func loadHeaders(mh textproto.MIMEHeader, unescape bool) headers {
	h := make(headers, 0, len(mh))
	h.load(mh, unescape)
	return h
}
