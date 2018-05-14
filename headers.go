package freeswitch

import (
	"net/textproto"
	"net/url"
	"strings"
)

type headers []*header

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

func (h headers) String() (str string) {
	for _, header := range h {
		str += header.String()
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

func (h *header) String() string {
	return h.name + ": " + h.value + "\n"
}

func loadHeaders(mh textproto.MIMEHeader, unescape bool) headers {
	h := make(headers, 0, len(mh))
	h.load(mh, unescape)
	return h
}
